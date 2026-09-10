BEGIN;
CREATE ROLE handdraw_billing_worker NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE ROLE handdraw_quota_worker NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT USAGE ON SCHEMA handdraw TO handdraw_billing_worker, handdraw_quota_worker;
CREATE TABLE handdraw.subscriptions (
 workspace_id text COLLATE "C" PRIMARY KEY REFERENCES handdraw.workspaces(id),
 provider text NOT NULL DEFAULT 'polar' CHECK(provider='polar'),
 provider_customer_id text, provider_subscription_id text UNIQUE, provider_status text,
 plan text NOT NULL CHECK(plan IN ('cloud','team')),
 billing_interval text NOT NULL CHECK(billing_interval IN ('month','year')),
 status text NOT NULL CHECK(status IN ('trialing','active','grace_period','suspended','ended')),
 paid_seats integer NOT NULL CHECK(paid_seats>=1),
 trial_ends_at timestamptz, current_period_start timestamptz, current_period_end timestamptz,
 paid_through_at timestamptz, last_successful_payment_at timestamptz,
 renewal_obligation_id text, renewal_failure_at timestamptz, grace_ends_at timestamptz,
 access_expires_at timestamptz, cancel_at_period_end boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(), updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 reconciled_at timestamptz,
 CHECK(plan<>'cloud' OR paid_seats=1), CHECK(status<>'trialing' OR paid_seats<=5),
 CHECK(current_period_end IS NULL OR current_period_start IS NULL OR current_period_end>current_period_start),
 CHECK(updated_at>=created_at)
);
CREATE TABLE handdraw.workspace_usage (
 workspace_id text COLLATE "C" PRIMARY KEY REFERENCES handdraw.workspaces(id),
 used_bytes bigint NOT NULL DEFAULT 0 CHECK(used_bytes>=0),
 reserved_bytes bigint NOT NULL DEFAULT 0 CHECK(reserved_bytes>=0),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
ALTER TABLE handdraw.subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE handdraw.workspace_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.workspace_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_subscription ON handdraw.subscriptions TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY migration_usage ON handdraw.workspace_usage TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_subscription ON handdraw.subscriptions FOR SELECT TO handdraw_access_owner USING(true);
CREATE POLICY access_usage ON handdraw.workspace_usage TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE POLICY billing_projection ON handdraw.subscriptions TO handdraw_billing_worker USING(true) WITH CHECK(true);
GRANT SELECT ON handdraw.subscriptions TO handdraw_access_owner;
GRANT SELECT, INSERT, UPDATE ON handdraw.workspace_usage TO handdraw_access_owner;
GRANT SELECT, INSERT ON handdraw.subscriptions TO handdraw_billing_worker;
GRANT UPDATE(provider_customer_id,provider_subscription_id,provider_status,plan,billing_interval,status,paid_seats,
 trial_ends_at,current_period_start,current_period_end,paid_through_at,last_successful_payment_at,
 renewal_obligation_id,renewal_failure_at,grace_ends_at,access_expires_at,cancel_at_period_end,revision,updated_at,reconciled_at)
 ON handdraw.subscriptions TO handdraw_billing_worker;
CREATE FUNCTION handdraw.is_workspace_owner(w text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT handdraw.is_workspace_member(w) AND EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=w AND owner_user_id=handdraw.current_actor())
$$;
CREATE FUNCTION handdraw.entitlement_editable(w text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM handdraw.subscriptions s JOIN handdraw.workspaces ws ON ws.id=s.workspace_id
 WHERE ws.id=w AND ws.lifecycle='ready' AND ws.deleted_at IS NULL
 AND ((ws.kind='personal' AND s.plan='cloud') OR (ws.kind='team' AND s.plan='team'))
 AND (SELECT count(*) FROM handdraw.workspace_members m WHERE m.workspace_id=w AND m.role IN ('owner','editor'))<=s.paid_seats
 AND ((s.status='trialing' AND s.trial_ends_at>statement_timestamp())
 OR (s.status='active' AND s.paid_through_at>statement_timestamp() AND s.last_successful_payment_at IS NOT NULL)
 OR (s.status='grace_period' AND s.grace_ends_at>statement_timestamp() AND s.last_successful_payment_at IS NOT NULL
 AND s.renewal_obligation_id IS NOT NULL AND s.renewal_failure_at IS NOT NULL
 AND s.grace_ends_at<=s.renewal_failure_at+interval '7 days')))
$$;
CREATE FUNCTION handdraw.can_read_workspace(w text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT handdraw.is_workspace_member(w) AND EXISTS(SELECT 1 FROM handdraw.workspaces ws JOIN handdraw.subscriptions s ON s.workspace_id=ws.id
 WHERE ws.id=w AND ws.lifecycle='ready' AND ws.deleted_at IS NULL AND
 (handdraw.entitlement_editable(w) OR s.access_expires_at>statement_timestamp()-interval '90 days'
 OR (s.status='active' AND s.paid_through_at>statement_timestamp()) OR (s.status='trialing' AND s.trial_ends_at>statement_timestamp())))
$$;
CREATE FUNCTION handdraw.can_write_workspace(w text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT handdraw.is_workspace_member(w) AND handdraw.entitlement_editable(w) AND EXISTS(SELECT 1 FROM handdraw.workspace_members
 WHERE workspace_id=w AND user_id=handdraw.current_actor() AND role IN ('owner','editor'))
$$;
CREATE FUNCTION handdraw.initialize_usage() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN INSERT INTO handdraw.workspace_usage(workspace_id) VALUES(NEW.id); RETURN NEW; END;
$$;
CREATE FUNCTION handdraw.check_subscription_plan() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;
BEGIN
 IF TG_TABLE_NAME='workspaces' THEN w:=NEW.id; ELSE w:=NEW.workspace_id; END IF;
 IF EXISTS(SELECT 1 FROM handdraw.subscriptions s JOIN handdraw.workspaces ws ON ws.id=s.workspace_id
 WHERE ws.id=w AND ((ws.kind='personal' AND s.plan<>'cloud') OR (ws.kind='team' AND s.plan<>'team'))) THEN
 RAISE EXCEPTION 'subscription plan must match workspace kind' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END;
$$;
CREATE FUNCTION handdraw.touch_subscription_access() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN UPDATE handdraw.workspaces SET access_revision=access_revision+1,updated_at=clock_timestamp() WHERE id=NEW.workspace_id; RETURN NEW; END;
$$;
CREATE FUNCTION handdraw.adjust_usage(w text, used_delta bigint, reserved_delta bigint, expected_revision bigint) RETURNS bigint
 LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE result bigint; capacity bigint;
BEGIN
 PERFORM 1 FROM handdraw.workspaces WHERE id=w FOR UPDATE;
 SELECT CASE WHEN plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*paid_seats END INTO capacity FROM handdraw.subscriptions WHERE workspace_id=w;
 UPDATE handdraw.workspace_usage SET used_bytes=used_bytes+used_delta,reserved_bytes=reserved_bytes+reserved_delta,
 revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=w AND revision=expected_revision
 AND (used_delta+reserved_delta<=0 OR used_bytes+used_delta+reserved_bytes+reserved_delta<=capacity)
 RETURNING revision INTO result;
 IF result IS NULL THEN RAISE EXCEPTION 'quota or revision conflict' USING ERRCODE='P0001'; END IF;
 RETURN result;
END;
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.is_workspace_owner(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.entitlement_editable(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.can_read_workspace(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.can_write_workspace(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.initialize_usage() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.check_subscription_plan() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.touch_subscription_access() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.adjust_usage(text,bigint,bigint,bigint) OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.is_workspace_owner(text),handdraw.entitlement_editable(text),handdraw.can_read_workspace(text),handdraw.can_write_workspace(text),
 handdraw.initialize_usage(),handdraw.check_subscription_plan(),handdraw.touch_subscription_access(),handdraw.adjust_usage(text,bigint,bigint,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.is_workspace_owner(text),handdraw.can_read_workspace(text),handdraw.can_write_workspace(text) TO handdraw_request;
GRANT EXECUTE ON FUNCTION handdraw.adjust_usage(text,bigint,bigint,bigint) TO handdraw_quota_worker;
CREATE POLICY subscription_owner_read ON handdraw.subscriptions FOR SELECT TO handdraw_request USING(handdraw.is_workspace_owner(workspace_id));
CREATE POLICY usage_member_read ON handdraw.workspace_usage FOR SELECT TO handdraw_request USING(handdraw.is_workspace_member(workspace_id));
GRANT SELECT ON handdraw.subscriptions, handdraw.workspace_usage TO handdraw_request;
CREATE TRIGGER workspace_usage_created AFTER INSERT ON handdraw.workspaces FOR EACH ROW EXECUTE FUNCTION handdraw.initialize_usage();
INSERT INTO handdraw.workspace_usage(workspace_id) SELECT id FROM handdraw.workspaces;
CREATE CONSTRAINT TRIGGER subscription_plan AFTER INSERT OR UPDATE ON handdraw.subscriptions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.check_subscription_plan();
CREATE CONSTRAINT TRIGGER workspace_subscription_plan AFTER UPDATE ON handdraw.workspaces DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.check_subscription_plan();
CREATE TRIGGER subscription_access_revision AFTER INSERT OR UPDATE ON handdraw.subscriptions FOR EACH ROW EXECUTE FUNCTION handdraw.touch_subscription_access();
COMMIT;
