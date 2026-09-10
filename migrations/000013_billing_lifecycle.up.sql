BEGIN;
-- Request credentials can only create reviewed intentions. The worker is function-only.
CREATE ROLE handdraw_billing_runtime NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT USAGE ON SCHEMA handdraw TO handdraw_billing_runtime;
ALTER TABLE handdraw.subscriptions DROP CONSTRAINT subscriptions_provider_check;
ALTER TABLE handdraw.subscriptions ADD CONSTRAINT subscriptions_provider_check CHECK(provider IN ('polar','local'));
GRANT INSERT,UPDATE ON handdraw.subscriptions TO handdraw_access_owner;
CREATE POLICY access_billing_manage ON handdraw.subscriptions TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT UPDATE(kind,lifecycle,deleted_at) ON handdraw.workspaces TO handdraw_access_owner;

CREATE TABLE handdraw.billing_change_intents (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'bci')),
 workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 actor_user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 idempotency_key uuid NOT NULL, request jsonb NOT NULL,
 kind text NOT NULL CHECK(kind IN ('checkout','plan_upgrade','plan_downgrade','seat_change','cancel','resume')),
 target_plan text NOT NULL CHECK(target_plan IN ('cloud','team')),
 billing_interval text NOT NULL CHECK(billing_interval IN ('month','year')),
 target_seats integer NOT NULL CHECK(target_seats BETWEEN 1 AND 100),
 source_subscription_id text, source_plan text, source_seats integer, source_period_end timestamptz, source_revision bigint NOT NULL, access_revision bigint NOT NULL,
 revision bigint NOT NULL DEFAULT 1, access_snapshot jsonb NOT NULL DEFAULT '{}', member_changes jsonb NOT NULL DEFAULT '{}',
 status text NOT NULL DEFAULT 'quoted' CHECK(status IN ('quoted','requested','awaiting_provider','scheduled','applied','failed','canceled')),
 phase text NOT NULL DEFAULT 'confirmation_required',
 amount_minor bigint NOT NULL CHECK(amount_minor>=0), recurring_minor bigint NOT NULL CHECK(recurring_minor>0),
 effective_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 confirmed_at timestamptz, trial_ends_at timestamptz,
 lease_token text, lease_until timestamptz, retry_at timestamptz NOT NULL DEFAULT statement_timestamp(), attempts integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 UNIQUE(actor_user_id,idempotency_key), CHECK(target_plan<>'cloud' OR target_seats=1)
);
CREATE UNIQUE INDEX billing_one_operation ON handdraw.billing_change_intents(workspace_id) WHERE status IN ('requested','awaiting_provider','scheduled');
CREATE TABLE handdraw.billing_contracts (
 provider_subscription_id text PRIMARY KEY, workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 intent_id text COLLATE "C" REFERENCES handdraw.billing_change_intents(id), version bigint NOT NULL DEFAULT 0,
 snapshot jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
-- Durable deterministic provider state; never accepted from a request principal or checkout redirect.
CREATE TABLE handdraw.local_billing_operations (
 intent_id text COLLATE "C" PRIMARY KEY REFERENCES handdraw.billing_change_intents(id),
 state jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
CREATE TABLE handdraw.provider_events (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'evt')),
 provider text NOT NULL CHECK(provider='local'), provider_event_id text NOT NULL,
 intent_id text COLLATE "C" NOT NULL REFERENCES handdraw.billing_change_intents(id),
 version bigint NOT NULL, received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 UNIQUE(provider,provider_event_id)
);
CREATE TABLE handdraw.payment_orders (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'ord')),
 workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 provider text NOT NULL CHECK(provider IN ('local','polar')), provider_order_id text NOT NULL,
 provider_subscription_id text NOT NULL REFERENCES handdraw.billing_contracts(provider_subscription_id),
 currency text NOT NULL CHECK(currency='USD'), amount_minor bigint NOT NULL CHECK(amount_minor>0),
 paid_at timestamptz NOT NULL, service_from timestamptz NOT NULL, service_until timestamptz NOT NULL CHECK(service_until>service_from),
 UNIQUE(provider,provider_order_id)
);
CREATE TABLE handdraw.payment_refunds (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'rfnd')),
 order_id text COLLATE "C" NOT NULL REFERENCES handdraw.payment_orders(id),
 provider_refund_id text NOT NULL UNIQUE, amount_minor bigint NOT NULL CHECK(amount_minor>0), refunded_at timestamptz NOT NULL
);
CREATE TABLE handdraw.retention_episodes (
 workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 access_ended_at timestamptz NOT NULL, purge_at timestamptz NOT NULL, checked_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 status text NOT NULL CHECK(status IN ('retaining','canceled','purging','purged')),
 PRIMARY KEY(workspace_id,access_ended_at), CHECK(purge_at=access_ended_at+interval '90 days')
);
CREATE TABLE handdraw.billing_notice_outbox (
 workspace_id text COLLATE "C" NOT NULL, access_ended_at timestamptz NOT NULL,
 milestone text NOT NULL CHECK(milestone IN ('started','30_days','7_days','1_day')),
 owner_user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id), purge_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 PRIMARY KEY(workspace_id,access_ended_at,milestone),
 FOREIGN KEY(workspace_id,access_ended_at) REFERENCES handdraw.retention_episodes(workspace_id,access_ended_at)
);

CREATE FUNCTION handdraw.billing_removals(w text) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT jsonb_build_object(
 'remove_user_ids',coalesce((SELECT jsonb_agg(user_id ORDER BY user_id) FROM handdraw.workspace_members WHERE workspace_id=w AND role<>'owner'),'[]'),
 'revoke_invitation_ids',coalesce((SELECT jsonb_agg(i.id ORDER BY i.id) FROM handdraw.invitations i WHERE i.workspace_id=w AND i.status='pending' AND (i.scope='workspace' OR EXISTS(SELECT 1 FROM handdraw.workspace_members m WHERE m.workspace_id=w AND m.role<>'owner' AND handdraw.member_email_matches(m.user_id,i.email_normalized)))),'[]'),
 'revoke_board_grants',coalesce((SELECT jsonb_agg(jsonb_build_object('board_id',g.board_id,'user_id',g.user_id) ORDER BY g.board_id,g.user_id) FROM handdraw.board_grants g JOIN handdraw.workspace_members m ON m.workspace_id=g.workspace_id AND m.user_id=g.user_id WHERE g.workspace_id=w AND m.role<>'owner'),'[]'))
$$;
CREATE FUNCTION handdraw.billing_access_snapshot(w text) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT jsonb_build_object('members',coalesce((SELECT jsonb_agg(jsonb_build_array(user_id,role,revision) ORDER BY user_id) FROM handdraw.workspace_members WHERE workspace_id=w),'[]'),
 'invitations',coalesce((SELECT jsonb_agg(jsonb_build_array(id,role,status,expires_at,board_id) ORDER BY id) FROM handdraw.invitations WHERE workspace_id=w),'[]'),
 'grants',coalesce((SELECT jsonb_agg(jsonb_build_array(board_id,user_id,role,expires_at) ORDER BY board_id,user_id) FROM handdraw.board_grants WHERE workspace_id=w),'[]'))
$$;
CREATE FUNCTION handdraw.billing_view(i text) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT jsonb_build_object('id',id,'kind',kind,'target_plan',target_plan,'billing_interval',billing_interval,'target_seats',target_seats,
 'intent_revision',b.revision::text,'subscription_revision',source_revision::text,'workspace_access_revision',access_revision::text,
 'source_plan',source_plan,'current_seats',source_seats,'status',status,'phase',phase,'effective_at',effective_at,'confirmed_at',confirmed_at,'member_changes',member_changes,
 'quote',jsonb_build_object('amount_due_minor',amount_minor,'recurring_amount_minor',recurring_minor,'currency','USD','expires_at',expires_at),
 'storage',jsonb_build_object('used_bytes',u.used_bytes,'reserved_bytes',u.reserved_bytes,'target_quota_bytes',CASE WHEN target_plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*target_seats END,
 'uploads_blocked_after_change',u.used_bytes+u.reserved_bytes>CASE WHEN target_plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*target_seats END),
 'provider','local','checkout_url',NULL,'capabilities',jsonb_build_object('live_payment',false,'portal',false))
 FROM handdraw.billing_change_intents b JOIN handdraw.workspace_usage u USING(workspace_id) WHERE b.id=i
$$;
CREATE FUNCTION handdraw.billing_request(w text,action text,candidate text,k uuid,p jsonb) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
#variable_conflict use_column
DECLARE ws handdraw.workspaces%ROWTYPE; s handdraw.subscriptions%ROWTYPE; i handdraw.billing_change_intents%ROWTYPE;
 plan text; cycle text; seats integer; amount bigint; due bigint; old_amount bigint; kind text; ending timestamptz; active boolean; allocated bigint;
BEGIN
 SELECT * INTO ws FROM handdraw.workspaces WHERE id=w FOR UPDATE;
 IF NOT FOUND OR NOT handdraw.is_workspace_member(w) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF action='subscription' THEN
  IF ws.owner_user_id<>handdraw.current_actor() THEN RETURN (SELECT to_jsonb(e) FROM handdraw.access_entitlement(w) e); END IF;
  RETURN jsonb_build_object('entitlement',(SELECT to_jsonb(e) FROM handdraw.access_entitlement(w) e),'subscription',(SELECT (to_jsonb(b)-'provider_customer_id'-'provider_subscription_id')||jsonb_build_object('revision',b.revision::text) FROM handdraw.subscriptions b WHERE workspace_id=w),'provider','local','live_payment',false);
 END IF;
 IF NOT handdraw.is_workspace_owner(w) THEN RAISE EXCEPTION 'owner required' USING ERRCODE='HD403'; END IF;
 IF action='history' THEN RETURN jsonb_build_object('orders',coalesce((SELECT jsonb_agg(to_jsonb(o)-'provider_subscription_id') FROM (SELECT * FROM handdraw.payment_orders WHERE workspace_id=w ORDER BY paid_at DESC,id DESC LIMIT 100) o),'[]'),'refunds',coalesce((SELECT jsonb_agg(to_jsonb(r)) FROM (SELECT r.* FROM handdraw.payment_refunds r JOIN handdraw.payment_orders o ON o.id=r.order_id WHERE o.workspace_id=w ORDER BY r.refunded_at DESC,r.id DESC LIMIT 100) r),'[]')); END IF;
 IF action='status' THEN
  IF NOT EXISTS(SELECT 1 FROM handdraw.billing_change_intents WHERE id=p->>'quote_id' AND workspace_id=w) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
  RETURN handdraw.billing_view(p->>'quote_id');
 END IF;
 IF ws.lifecycle<>'ready' OR ws.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'purging or deleted' USING ERRCODE='HD409'; END IF;
 SELECT * INTO s FROM handdraw.subscriptions WHERE workspace_id=w;
 active:=handdraw.entitlement_editable(w);
 ending:=coalesce(s.grace_ends_at,s.paid_through_at,s.trial_ends_at,s.access_expires_at,clock_timestamp());
 -- An idempotency key identifies the exact original quote request, never a provider charge.
 IF action IN ('quote','checkout','cancel','resume') AND coalesce(p->>'plan_change_id','')='' THEN
  SELECT * INTO i FROM handdraw.billing_change_intents WHERE actor_user_id=handdraw.current_actor() AND idempotency_key=k;
  IF FOUND THEN
   IF i.workspace_id<>w OR i.request<>jsonb_build_object('action',action,'body',p) THEN RAISE EXCEPTION 'idempotency conflict' USING ERRCODE='HD409'; END IF;
   RETURN handdraw.billing_view(i.id);
  END IF;
 END IF;
 IF action IN ('confirm','checkout') AND coalesce(p->>'quote_id',p->>'plan_change_id','')<>'' THEN
  SELECT * INTO i FROM handdraw.billing_change_intents WHERE id=coalesce(p->>'quote_id',p->>'plan_change_id') AND workspace_id=w FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
  IF i.status='applied' THEN RETURN handdraw.billing_view(i.id); END IF;
  IF i.actor_user_id<>ws.owner_user_id OR (p->>'intent_revision') IS NULL OR i.revision<>(p->>'intent_revision')::bigint OR i.expires_at<=clock_timestamp() OR i.access_revision<>ws.access_revision OR i.source_revision<>coalesce(s.revision,0) THEN RAISE EXCEPTION 'plan change stale' USING ERRCODE='HD409'; END IF;
  IF i.kind='plan_downgrade' AND (NOT coalesce((p->>'confirm_member_changes')::boolean,false) OR i.member_changes<>handdraw.billing_removals(w)) THEN RAISE EXCEPTION 'confirmation required' USING ERRCODE='HD409'; END IF;
  IF i.kind='seat_change' AND i.target_seats<s.paid_seats THEN
   IF jsonb_typeof(coalesce(p->'demote_user_ids','[]'))<>'array' OR jsonb_typeof(coalesce(p->'revoke_invitation_ids','[]'))<>'array' THEN RAISE EXCEPTION 'member plan required' USING ERRCODE='HD400'; END IF;
   IF (SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=w AND role='editor' AND user_id IN (SELECT jsonb_array_elements_text(coalesce(p->'demote_user_ids','[]'))))<>jsonb_array_length(coalesce(p->'demote_user_ids','[]')) OR
    (SELECT count(*) FROM handdraw.invitations WHERE workspace_id=w AND scope='workspace' AND role='editor' AND status='pending' AND expires_at>clock_timestamp() AND id IN (SELECT jsonb_array_elements_text(coalesce(p->'revoke_invitation_ids','[]'))))<>jsonb_array_length(coalesce(p->'revoke_invitation_ids','[]')) THEN RAISE EXCEPTION 'invalid member plan' USING ERRCODE='HD409'; END IF;
   SELECT (SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=w AND role IN ('owner','editor'))+(SELECT count(*) FROM handdraw.invitations WHERE workspace_id=w AND scope='workspace' AND role='editor' AND status='pending' AND expires_at>clock_timestamp()) INTO allocated;
   IF allocated-jsonb_array_length(coalesce(p->'demote_user_ids','[]'))-jsonb_array_length(coalesce(p->'revoke_invitation_ids','[]'))>i.target_seats THEN RAISE EXCEPTION 'members exceed target seats' USING ERRCODE='HD409'; END IF;
   UPDATE handdraw.billing_change_intents SET member_changes=jsonb_build_object('demote_user_ids',coalesce(p->'demote_user_ids','[]'),'revoke_invitation_ids',coalesce(p->'revoke_invitation_ids','[]')) WHERE id=i.id;
  END IF;
  IF action='checkout' AND (i.kind<>'plan_downgrade' OR s.status<>'ended' OR ending>clock_timestamp() OR i.confirmed_at IS NULL) THEN RAISE EXCEPTION 'team must end first' USING ERRCODE='HD409'; END IF;
  IF i.status NOT IN ('quoted','requested','scheduled','awaiting_provider') THEN RAISE EXCEPTION 'intent conflict' USING ERRCODE='HD409'; END IF;
  UPDATE handdraw.billing_change_intents SET confirmed_at=clock_timestamp(),status=CASE WHEN (kind='plan_downgrade' OR kind='seat_change' AND target_seats<source_seats) AND action='confirm' THEN 'scheduled' ELSE 'requested' END,
   phase=CASE WHEN kind='plan_downgrade' AND action='confirm' THEN CASE WHEN s.status='ended' AND ending<=clock_timestamp() THEN 'checkout_ready' ELSE 'awaiting_team_end' END ELSE CASE WHEN kind='seat_change' AND target_seats<source_seats THEN 'awaiting_period_end' ELSE 'awaiting_payment' END END,
   retry_at=clock_timestamp(),lease_until=NULL WHERE id=i.id;
  RETURN handdraw.billing_view(i.id);
 END IF;
 IF action NOT IN ('quote','checkout','cancel','resume') THEN RAISE EXCEPTION 'invalid action' USING ERRCODE='HD400'; END IF;
 plan:=coalesce(p->>'target_plan',p->>'plan',s.plan); cycle:=coalesce(p->>'billing_interval',s.billing_interval); seats:=coalesce((p->>'editor_seats')::integer,s.paid_seats);
 IF plan NOT IN ('cloud','team') OR cycle NOT IN ('month','year') OR seats NOT BETWEEN 1 AND 100 OR (plan='cloud' AND seats<>1) OR plan IS NULL OR seats IS NULL THEN RAISE EXCEPTION 'invalid plan' USING ERRCODE='HD400'; END IF;
 kind:=CASE WHEN action IN ('cancel','resume') THEN action WHEN s.workspace_id IS NULL OR NOT active AND s.plan=plan THEN 'checkout' WHEN s.plan='cloud' AND plan='team' THEN 'plan_upgrade' WHEN s.plan='team' AND plan='cloud' THEN 'plan_downgrade' ELSE 'seat_change' END;
 IF coalesce(p->>'plan_change_id','')<>'' THEN
  SELECT * INTO i FROM handdraw.billing_change_intents WHERE id=p->>'plan_change_id' AND workspace_id=w AND status IN ('requested','awaiting_provider','scheduled');
  IF NOT FOUND OR i.target_plan<>plan OR i.target_seats<>seats OR i.billing_interval<>cycle THEN RAISE EXCEPTION 'invalid refresh' USING ERRCODE='HD409'; END IF;
  kind:=i.kind;
 END IF;
 IF kind='checkout' AND ((ws.kind='personal')<>(plan='cloud') OR active) THEN RAISE EXCEPTION 'use plan quote' USING ERRCODE='HD409'; END IF;
 IF kind='plan_upgrade' AND (NOT active OR s.cancel_at_period_end) AND coalesce(p->>'plan_change_id','')='' THEN RAISE EXCEPTION 'source not eligible' USING ERRCODE='HD409'; END IF;
 IF kind='seat_change' AND (plan<>'team' OR NOT active AND coalesce(p->>'plan_change_id','')='' OR seats=s.paid_seats) THEN RAISE EXCEPTION 'seat change unavailable' USING ERRCODE='HD409'; END IF;
 -- Decreases remain pending until the paid period ends and the reviewed member plan still matches.
 IF kind IN ('seat_change','plan_upgrade') AND cycle<>s.billing_interval THEN RAISE EXCEPTION 'billing cycle change unavailable' USING ERRCODE='HD409'; END IF;
 SELECT (SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=w AND role IN ('owner','editor'))+(SELECT count(*) FROM handdraw.invitations WHERE workspace_id=w AND scope='workspace' AND role='editor' AND status='pending' AND expires_at>clock_timestamp()) INTO allocated;
 IF kind NOT IN ('plan_downgrade','seat_change') AND seats<allocated THEN RAISE EXCEPTION 'seats allocated' USING ERRCODE='HD409'; END IF;
 IF s.status='trialing' AND active AND seats>5 THEN RAISE EXCEPTION 'trial capacity' USING ERRCODE='HD409'; END IF;
 IF EXISTS(SELECT 1 FROM handdraw.billing_change_intents WHERE workspace_id=w AND status IN ('requested','awaiting_provider','scheduled') AND id IS DISTINCT FROM p->>'plan_change_id') THEN RAISE EXCEPTION 'operation in progress' USING ERRCODE='HD409'; END IF;
 amount:=CASE WHEN plan='cloud' THEN CASE WHEN cycle='month' THEN 800 ELSE 7200 END ELSE seats*CASE WHEN cycle='month' THEN 1200 ELSE 12000 END END;
 due:=amount;
 IF kind IN ('plan_upgrade','seat_change') AND s.status<>'trialing' AND seats>=s.paid_seats THEN
  old_amount:=CASE WHEN s.plan='cloud' THEN CASE WHEN cycle='month' THEN 800 ELSE 7200 END ELSE s.paid_seats*CASE WHEN cycle='month' THEN 1200 ELSE 12000 END END;
  IF s.current_period_start IS NULL OR s.paid_through_at IS NULL OR s.paid_through_at<=s.current_period_start THEN RAISE EXCEPTION 'paid period evidence required' USING ERRCODE='HD409'; END IF;
  due:=greatest(0,ceil((amount-old_amount)*extract(epoch FROM (s.paid_through_at-clock_timestamp()))/extract(epoch FROM (s.paid_through_at-s.current_period_start))))::bigint;
 END IF;
 IF coalesce(p->>'plan_change_id','')<>'' THEN
  SELECT * INTO i FROM handdraw.billing_change_intents WHERE id=p->>'plan_change_id' AND workspace_id=w AND kind IN ('plan_downgrade','plan_upgrade','seat_change','checkout') AND status IN ('scheduled','requested','awaiting_provider') FOR UPDATE;
  IF NOT FOUND OR plan<>i.target_plan OR seats<>i.target_seats OR cycle<>i.billing_interval THEN RAISE EXCEPTION 'cannot refresh' USING ERRCODE='HD409'; END IF;
  UPDATE handdraw.billing_change_intents SET access_revision=ws.access_revision,access_snapshot=handdraw.billing_access_snapshot(w),source_revision=s.revision,revision=revision+1,member_changes=CASE WHEN kind='plan_downgrade' THEN handdraw.billing_removals(w) ELSE member_changes END,confirmed_at=NULL,expires_at=clock_timestamp()+interval '10 minutes',amount_minor=CASE WHEN kind='plan_downgrade' THEN amount ELSE amount_minor END,recurring_minor=amount,phase='confirmation_required' WHERE id=i.id;
  RETURN handdraw.billing_view(i.id);
 END IF;
 INSERT INTO handdraw.billing_change_intents(id,workspace_id,actor_user_id,idempotency_key,request,kind,target_plan,billing_interval,target_seats,source_subscription_id,source_plan,source_seats,source_period_end,source_revision,access_revision,access_snapshot,member_changes,amount_minor,recurring_minor,effective_at,expires_at,trial_ends_at,status,phase,confirmed_at)
 VALUES(candidate,w,ws.owner_user_id,k,jsonb_build_object('action',action,'body',p),kind,plan,cycle,seats,s.provider_subscription_id,s.plan,s.paid_seats,coalesce(s.paid_through_at,s.trial_ends_at),coalesce(s.revision,0),ws.access_revision,handdraw.billing_access_snapshot(w),CASE WHEN kind='plan_downgrade' THEN handdraw.billing_removals(w) ELSE '{}' END,
 CASE WHEN kind IN ('cancel','resume','plan_downgrade') OR kind='seat_change' AND seats<s.paid_seats OR s.status='trialing' AND active THEN 0 ELSE due END,amount,
 CASE WHEN kind='plan_downgrade' OR kind='seat_change' AND seats<s.paid_seats THEN ending ELSE clock_timestamp() END,clock_timestamp()+interval '10 minutes',CASE WHEN s.status='trialing' AND active THEN s.trial_ends_at END,
 CASE WHEN action IN ('checkout','cancel','resume') THEN 'requested' ELSE 'quoted' END,CASE WHEN action IN ('checkout','cancel','resume') THEN 'awaiting_payment' ELSE 'confirmation_required' END,
 CASE WHEN action IN ('checkout','cancel','resume') THEN clock_timestamp() END);
 RETURN handdraw.billing_view(candidate);
END; $$;

CREATE FUNCTION handdraw.lease_billing(token text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE i handdraw.billing_change_intents%ROWTYPE;
BEGIN
 IF token!~'^[a-f0-9]{64}$' THEN RAISE EXCEPTION 'invalid lease' USING ERRCODE='HD400'; END IF;
 SELECT * INTO i FROM handdraw.billing_change_intents WHERE status IN ('requested','awaiting_provider','scheduled','applied') AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN NULL; END IF;
 UPDATE handdraw.billing_change_intents SET lease_token=token,lease_until=clock_timestamp()+interval '2 minutes',attempts=attempts+1,retry_at=clock_timestamp()+interval '30 seconds' WHERE id=i.id;
 RETURN jsonb_build_object('id',i.id,'token',token);
END; $$;
CREATE FUNCTION handdraw.local_billing_prepare(i text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE; result jsonb;
BEGIN
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i;
 IF NOT FOUND OR r.status NOT IN ('requested','awaiting_provider','scheduled','applied') THEN RAISE EXCEPTION 'invalid operation' USING ERRCODE='HD409'; END IF;
 INSERT INTO handdraw.local_billing_operations(intent_id,state) VALUES(i,jsonb_build_object('version',1,'subscription_id','local_'||i,'status','pending','card_verified',false,'orders','[]'::jsonb,'refunds','[]'::jsonb)) ON CONFLICT DO NOTHING;
 IF FOUND AND (r.kind IN ('cancel','resume') OR r.kind='plan_downgrade' AND r.phase='awaiting_team_end') THEN
  UPDATE handdraw.local_billing_operations op SET state=op.state||jsonb_build_object('cancel_at_period_end',r.kind<>'resume','version',(op.state->>'version')::bigint+1)
  WHERE op.intent_id=(SELECT intent_id FROM handdraw.billing_contracts WHERE provider_subscription_id=r.source_subscription_id);
 END IF;
 SELECT state INTO result FROM handdraw.local_billing_operations WHERE intent_id=i;
 RETURN result;
END; $$;
-- Internal fixture control: monotonic canonical state and a deduplicated delivery inbox commit together.
CREATE FUNCTION handdraw.local_billing_event(i text,event_id text,provider_event text,p jsonb) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE old jsonb;
BEGIN
 SELECT state INTO old FROM handdraw.local_billing_operations WHERE intent_id=i FOR UPDATE;
 IF NOT FOUND OR (p->>'version')::bigint<1 OR p->>'subscription_id'<>'local_'||i THEN RAISE EXCEPTION 'invalid provider state' USING ERRCODE='HD400'; END IF;
 INSERT INTO handdraw.provider_events(id,provider,provider_event_id,intent_id,version) VALUES(event_id,'local',provider_event,i,(p->>'version')::bigint) ON CONFLICT(provider,provider_event_id) DO NOTHING;
 IF NOT FOUND THEN RETURN; END IF;
 IF (p->>'version')::bigint>(old->>'version')::bigint THEN
  UPDATE handdraw.local_billing_operations SET state=p WHERE intent_id=i;
  UPDATE handdraw.billing_change_intents SET retry_at=clock_timestamp() WHERE id=i;
 END IF;
END; $$;
CREATE FUNCTION handdraw.apply_billing(i text,token text,p jsonb) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE; ws handdraw.workspaces%ROWTYPE; s handdraw.subscriptions%ROWTYPE; previous handdraw.billing_contracts%ROWTYPE;
 sid text:=p->>'subscription_id'; ver bigint:=(p->>'version')::bigint; state text:=p->>'status'; deadline timestamptz; failure timestamptz;
 item jsonb; order_key text; refunded bigint; first_trial boolean;
BEGIN
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 SELECT * INTO ws FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE;
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i FOR UPDATE;
 IF r.lease_token IS DISTINCT FROM token OR r.lease_until<=clock_timestamp() THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='HD409'; END IF;
 UPDATE handdraw.billing_change_intents SET lease_token=NULL,lease_until=NULL,retry_at=clock_timestamp()+interval '30 seconds' WHERE id=i;
 IF sid<>'local_'||i OR ver IS DISTINCT FROM (SELECT (op.state->>'version')::bigint FROM handdraw.local_billing_operations op WHERE intent_id=i) THEN RAISE EXCEPTION 'unverified snapshot' USING ERRCODE='HD409'; END IF;
 SELECT op.state INTO p FROM handdraw.local_billing_operations op WHERE intent_id=i;
 state:=p->>'status';
 SELECT * INTO s FROM handdraw.subscriptions WHERE workspace_id=r.workspace_id;
 SELECT * INTO previous FROM handdraw.billing_contracts WHERE provider_subscription_id=sid;
 IF previous.version>ver THEN RETURN; END IF;
 IF state NOT IN ('pending','trialing','active','renewal_failed','ended','failed') THEN RAISE EXCEPTION 'invalid state' USING ERRCODE='HD400'; END IF;
 first_trial:=s.workspace_id IS NULL AND NOT EXISTS(SELECT 1 FROM handdraw.billing_contracts WHERE workspace_id=r.workspace_id AND snapshot->>'status' IN ('trialing','active'));
 INSERT INTO handdraw.billing_contracts(provider_subscription_id,workspace_id,intent_id,version,snapshot) VALUES(sid,r.workspace_id,i,ver,p)
 ON CONFLICT(provider_subscription_id) DO UPDATE SET version=excluded.version,snapshot=excluded.snapshot,updated_at=clock_timestamp();
 FOR item IN SELECT value FROM jsonb_array_elements(p->'orders') LOOP
  INSERT INTO handdraw.payment_orders(id,workspace_id,provider,provider_order_id,provider_subscription_id,currency,amount_minor,paid_at,service_from,service_until)
  VALUES(item->>'id',r.workspace_id,'local',item->>'provider_order_id',sid,'USD',(item->>'amount_minor')::bigint,(item->>'paid_at')::timestamptz,(item->>'service_from')::timestamptz,(item->>'service_until')::timestamptz) ON CONFLICT(provider,provider_order_id) DO NOTHING;
  IF NOT EXISTS(SELECT 1 FROM handdraw.payment_orders WHERE provider='local' AND provider_order_id=item->>'provider_order_id' AND workspace_id=r.workspace_id AND provider_subscription_id=sid AND amount_minor=(item->>'amount_minor')::bigint AND paid_at=(item->>'paid_at')::timestamptz AND service_from=(item->>'service_from')::timestamptz AND service_until=(item->>'service_until')::timestamptz) THEN RAISE EXCEPTION 'order identity conflict' USING ERRCODE='HD409'; END IF;
 END LOOP;
 FOR item IN SELECT value FROM jsonb_array_elements(p->'refunds') LOOP
  SELECT id INTO order_key FROM handdraw.payment_orders WHERE provider_order_id=item->>'provider_order_id' AND provider_subscription_id=sid FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'refund order missing' USING ERRCODE='HD409'; END IF;
  INSERT INTO handdraw.payment_refunds(id,order_id,provider_refund_id,amount_minor,refunded_at) VALUES(item->>'id',order_key,item->>'provider_refund_id',(item->>'amount_minor')::bigint,(item->>'refunded_at')::timestamptz) ON CONFLICT(provider_refund_id) DO NOTHING;
  IF NOT EXISTS(SELECT 1 FROM handdraw.payment_refunds WHERE provider_refund_id=item->>'provider_refund_id' AND order_id=order_key AND amount_minor=(item->>'amount_minor')::bigint) THEN RAISE EXCEPTION 'refund identity conflict' USING ERRCODE='HD409'; END IF;
  SELECT sum(amount_minor) INTO refunded FROM handdraw.payment_refunds WHERE order_id=order_key;
  IF refunded>(SELECT amount_minor FROM handdraw.payment_orders WHERE id=order_key) THEN RAISE EXCEPTION 'refund exceeds order' USING ERRCODE='HD409'; END IF;
 END LOOP;
 -- Money history survives late delivery, but a purging workspace or superseded contract cannot regain content access.
 IF ws.lifecycle<>'ready' OR ws.deleted_at IS NOT NULL THEN UPDATE handdraw.billing_change_intents SET phase='payment_requires_review' WHERE id=i; RETURN; END IF;
 IF r.status='applied' AND s.provider_subscription_id IS DISTINCT FROM sid THEN RETURN; END IF;
 IF previous.version=ver AND r.status='applied' THEN RETURN; END IF;
 IF r.actor_user_id<>ws.owner_user_id THEN UPDATE handdraw.billing_change_intents SET phase='confirmation_required' WHERE id=i; RETURN; END IF;
 IF r.kind IN ('cancel','resume') THEN
  IF s.provider_subscription_id IS DISTINCT FROM r.source_subscription_id OR (s.revision<>r.source_revision AND s.cancel_at_period_end IS DISTINCT FROM (r.kind='cancel')) THEN UPDATE handdraw.billing_change_intents SET status='failed',phase='source_changed' WHERE id=i; RETURN; END IF;
  UPDATE handdraw.subscriptions SET cancel_at_period_end=(r.kind='cancel'),revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id;
  UPDATE handdraw.billing_change_intents SET status='applied',phase='complete' WHERE id=i; RETURN;
 END IF;
 IF r.kind='plan_downgrade' AND r.status='scheduled' AND r.phase='awaiting_team_end' THEN
  IF s.provider_subscription_id IS DISTINCT FROM r.source_subscription_id THEN RETURN; END IF;
  IF NOT s.cancel_at_period_end THEN UPDATE handdraw.subscriptions SET cancel_at_period_end=true,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id; END IF;
  RETURN;
 END IF;
 IF state IN ('pending','failed') THEN
  IF r.status='requested' THEN UPDATE handdraw.billing_change_intents SET status='awaiting_provider',phase='awaiting_payment' WHERE id=i; END IF;
  RETURN;
 END IF;
 IF r.status<>'applied' THEN
  IF r.confirmed_at IS NULL OR ((r.access_revision<>ws.access_revision OR r.source_revision<>coalesce(s.revision,0)) AND NOT (r.kind='seat_change' AND r.target_seats<r.source_seats AND r.effective_at<=clock_timestamp() AND s.provider_subscription_id IS NOT DISTINCT FROM r.source_subscription_id AND s.paid_seats=r.source_seats AND s.plan=r.source_plan AND s.billing_interval=r.billing_interval AND s.paid_through_at=r.source_period_end AND s.grace_ends_at IS NULL AND s.status IN ('active','ended') AND r.access_snapshot=handdraw.billing_access_snapshot(r.workspace_id))) THEN UPDATE handdraw.billing_change_intents SET phase='confirmation_required' WHERE id=i; RETURN; END IF;
  IF r.kind='plan_downgrade' THEN
   IF s.status<>'ended' OR coalesce(s.access_expires_at,s.paid_through_at,s.trial_ends_at)>clock_timestamp() OR r.member_changes<>handdraw.billing_removals(r.workspace_id) OR state<>'active' THEN UPDATE handdraw.billing_change_intents SET phase='confirmation_required' WHERE id=i; RETURN; END IF;
  END IF;
 END IF;
 IF state='trialing' THEN
  deadline:=(p->>'trial_ends_at')::timestamptz;
  IF NOT coalesce((p->>'card_verified')::boolean,false) OR deadline IS NULL OR deadline<=clock_timestamp() OR r.target_seats>5 OR
   (r.trial_ends_at IS NOT NULL AND deadline<>r.trial_ends_at) OR (r.trial_ends_at IS NULL AND (NOT first_trial AND s.provider_subscription_id IS DISTINCT FROM sid OR deadline>r.created_at+interval '7 days')) THEN RAISE EXCEPTION 'trial not eligible' USING ERRCODE='HD409'; END IF;
 ELSIF state='active' THEN
  deadline:=(p->>'paid_through_at')::timestamptz;
  IF deadline IS NULL OR deadline<=clock_timestamp() OR (s.provider_subscription_id=sid AND deadline<s.paid_through_at) OR (r.kind IN ('plan_upgrade','seat_change') AND r.target_seats>=r.source_seats AND r.trial_ends_at IS NULL AND r.status<>'applied' AND deadline IS DISTINCT FROM r.source_period_end) OR NOT EXISTS(SELECT 1 FROM handdraw.payment_orders WHERE provider_subscription_id=sid AND paid_at<=clock_timestamp() AND service_until=deadline AND amount_minor>=CASE WHEN r.status='applied' OR r.trial_ends_at IS NOT NULL OR r.target_seats<r.source_seats THEN r.recurring_minor ELSE r.amount_minor END) THEN RAISE EXCEPTION 'payment evidence missing' USING ERRCODE='HD409'; END IF;
 ELSE
  IF s.provider_subscription_id IS DISTINCT FROM sid THEN RETURN; END IF;
  IF state='renewal_failed' AND s.last_successful_payment_at IS NOT NULL THEN
   failure:=coalesce(s.renewal_failure_at,(p->>'renewal_failure_at')::timestamptz);
   IF failure IS NULL OR failure>clock_timestamp() OR failure<s.paid_through_at OR coalesce(p->>'renewal_obligation_id','')='' THEN RAISE EXCEPTION 'renewal evidence missing' USING ERRCODE='HD409'; END IF;
   deadline:=failure+interval '7 days';
   UPDATE handdraw.retention_episodes SET status='canceled' WHERE workspace_id=r.workspace_id AND status='retaining' AND access_ended_at<>deadline;
   UPDATE handdraw.subscriptions SET status='grace_period',renewal_failure_at=failure,renewal_obligation_id=coalesce(renewal_obligation_id,p->>'renewal_obligation_id'),grace_ends_at=deadline,access_expires_at=deadline,revision=revision+1,updated_at=clock_timestamp(),reconciled_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND (renewal_failure_at IS NULL OR status<>'grace_period');
  ELSE
   deadline:=coalesce(s.grace_ends_at,s.paid_through_at,s.trial_ends_at,s.access_expires_at);
   UPDATE handdraw.subscriptions SET status=CASE WHEN grace_ends_at>clock_timestamp() THEN 'grace_period' WHEN deadline>clock_timestamp() THEN status ELSE 'ended' END,access_expires_at=deadline,revision=revision+1,updated_at=clock_timestamp(),reconciled_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND (access_expires_at IS DISTINCT FROM deadline OR status<>'ended' AND deadline<=clock_timestamp());
  END IF;
  RETURN;
 END IF;
 IF previous.version=ver AND r.status='applied' THEN RETURN; END IF;
 IF r.kind='seat_change' AND r.target_seats<r.source_seats AND r.status<>'applied' THEN
  IF r.effective_at>clock_timestamp() OR state<>'active' THEN RETURN; END IF;
  UPDATE handdraw.workspace_members SET role='viewer',revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND role='editor' AND user_id IN (SELECT jsonb_array_elements_text(r.member_changes->'demote_user_ids'));
  UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id AND status='pending' AND id IN (SELECT jsonb_array_elements_text(r.member_changes->'revoke_invitation_ids'));
 END IF;
 IF r.kind='plan_downgrade' AND r.status<>'applied' THEN
  DELETE FROM handdraw.board_grants g WHERE g.workspace_id=r.workspace_id AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.member_changes->'revoke_board_grants') x WHERE x->>'board_id'=g.board_id AND x->>'user_id'=g.user_id);
  UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE id IN (SELECT jsonb_array_elements_text(r.member_changes->'revoke_invitation_ids')) AND workspace_id=r.workspace_id AND status='pending';
  DELETE FROM handdraw.workspace_members WHERE workspace_id=r.workspace_id AND user_id IN (SELECT jsonb_array_elements_text(r.member_changes->'remove_user_ids')) AND role<>'owner';
 END IF;
 UPDATE handdraw.workspaces SET kind=CASE WHEN r.target_plan='cloud' THEN 'personal' ELSE 'team' END,access_revision=access_revision+1,updated_at=clock_timestamp() WHERE id=r.workspace_id;
 INSERT INTO handdraw.subscriptions(workspace_id,provider,provider_subscription_id,provider_status,plan,billing_interval,status,paid_seats,trial_ends_at,paid_through_at,last_successful_payment_at,access_expires_at,cancel_at_period_end,reconciled_at,current_period_start,current_period_end)
 VALUES(r.workspace_id,'local',sid,state,r.target_plan,r.billing_interval,state,r.target_seats,CASE WHEN state='trialing' THEN deadline END,CASE WHEN state='active' THEN deadline END,CASE WHEN state='active' THEN (SELECT max(paid_at) FROM handdraw.payment_orders WHERE provider_subscription_id=sid) END,deadline,coalesce((p->>'cancel_at_period_end')::boolean,false),clock_timestamp(),(SELECT min(service_from) FROM handdraw.payment_orders WHERE provider_subscription_id=sid AND service_until=deadline),deadline)
 ON CONFLICT(workspace_id) DO UPDATE SET provider='local',provider_subscription_id=sid,provider_status=state,plan=r.target_plan,billing_interval=r.billing_interval,status=state,paid_seats=r.target_seats,trial_ends_at=excluded.trial_ends_at,paid_through_at=excluded.paid_through_at,current_period_start=excluded.current_period_start,current_period_end=deadline,last_successful_payment_at=excluded.last_successful_payment_at,access_expires_at=deadline,renewal_obligation_id=NULL,renewal_failure_at=NULL,grace_ends_at=NULL,cancel_at_period_end=excluded.cancel_at_period_end,revision=handdraw.subscriptions.revision+1,updated_at=clock_timestamp(),reconciled_at=clock_timestamp();
 UPDATE handdraw.billing_change_intents SET status='applied',phase='complete' WHERE id=i;
 UPDATE handdraw.retention_episodes SET status='canceled' WHERE workspace_id=r.workspace_id AND status='retaining';
END; $$;

CREATE FUNCTION handdraw.billing_maintenance() RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE ws handdraw.workspaces%ROWTYPE; s handdraw.subscriptions%ROWTYPE; deadline timestamptz; episode handdraw.retention_episodes%ROWTYPE; b text; count_done integer:=0;
BEGIN
 FOR ws IN SELECT w.* FROM handdraw.workspaces w JOIN handdraw.subscriptions sub ON sub.workspace_id=w.id WHERE w.lifecycle<>'deleted' AND (w.lifecycle='purging' OR coalesce(sub.grace_ends_at,sub.paid_through_at,sub.trial_ends_at,sub.access_expires_at)<=clock_timestamp()) ORDER BY (SELECT max(e.checked_at) FROM handdraw.retention_episodes e WHERE e.workspace_id=w.id) NULLS FIRST,w.id LIMIT 20 FOR UPDATE OF w SKIP LOCKED LOOP
  UPDATE handdraw.retention_episodes SET checked_at=clock_timestamp() WHERE workspace_id=ws.id;
  SELECT * INTO s FROM handdraw.subscriptions WHERE workspace_id=ws.id;
  deadline:=coalesce(s.grace_ends_at,s.paid_through_at,s.trial_ends_at,s.access_expires_at);
  IF ws.lifecycle='ready' AND handdraw.entitlement_editable(ws.id) THEN CONTINUE; END IF;
  IF ws.lifecycle='ready' THEN
   IF s.status<>'ended' OR s.access_expires_at IS DISTINCT FROM deadline THEN UPDATE handdraw.subscriptions SET status='ended',access_expires_at=deadline,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=ws.id; END IF;
   UPDATE handdraw.retention_episodes SET status='canceled' WHERE workspace_id=ws.id AND status='retaining' AND access_ended_at<>deadline;
   INSERT INTO handdraw.retention_episodes(workspace_id,access_ended_at,purge_at,status) VALUES(ws.id,deadline,deadline+interval '90 days','retaining') ON CONFLICT DO NOTHING;
   SELECT * INTO episode FROM handdraw.retention_episodes WHERE workspace_id=ws.id AND access_ended_at=deadline;
   IF episode.status<>'retaining' THEN CONTINUE; END IF;
   INSERT INTO handdraw.billing_notice_outbox(workspace_id,access_ended_at,milestone,owner_user_id,purge_at)
   SELECT ws.id,deadline,m.label,ws.owner_user_id,episode.purge_at FROM (VALUES('started',interval '90 days'),('30_days',interval '30 days'),('7_days',interval '7 days'),('1_day',interval '1 day')) m(label,remaining)
   WHERE clock_timestamp()>=episode.purge_at-m.remaining AND clock_timestamp()<episode.purge_at
   ON CONFLICT DO NOTHING;
   IF episode.purge_at<=clock_timestamp() THEN
    UPDATE handdraw.workspaces SET lifecycle='purging',access_revision=access_revision+1,updated_at=clock_timestamp() WHERE id=ws.id;
    UPDATE handdraw.retention_episodes SET status='purging' WHERE workspace_id=ws.id AND access_ended_at=deadline;
   END IF;
  ELSE
   -- Workspace lock is the irreversible barrier. Existing bounded Garage cleanup must verify object absence first.
   UPDATE handdraw.assets SET status='deleting' WHERE workspace_id=ws.id AND status NOT IN ('deleted','deleting');
   UPDATE handdraw.transfer_jobs SET status='failed',error_code='workspace_purging',input_state=NULL,lease_token=NULL,lease_until=NULL,completed_at=clock_timestamp() WHERE workspace_id=ws.id AND (status IN ('queued','running') OR input_state IS NOT NULL);
   IF EXISTS(SELECT 1 FROM handdraw.assets WHERE workspace_id=ws.id AND status<>'deleted') THEN CONTINUE; END IF;
   SELECT id INTO b FROM handdraw.boards WHERE workspace_id=ws.id ORDER BY id LIMIT 1;
   IF FOUND THEN
    DELETE FROM handdraw.board_documents WHERE board_id=b;
    DELETE FROM handdraw.boards WHERE id=b;
   ELSE
    DELETE FROM handdraw.projects WHERE workspace_id=ws.id;
    UPDATE handdraw.workspaces SET lifecycle='deleted',deleted_at=clock_timestamp(),access_revision=access_revision+1,updated_at=clock_timestamp() WHERE id=ws.id;
    UPDATE handdraw.retention_episodes SET status='purged' WHERE workspace_id=ws.id AND status='purging';
   END IF;
  END IF;
  count_done:=count_done+1;
 END LOOP;
 RETURN count_done;
END; $$;
-- Delivery is a local, queryable outbox. Recheck the current episode and Owner before returning a notice.
CREATE FUNCTION handdraw.local_billing_notices() RETURNS SETOF handdraw.billing_notice_outbox LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT n.* FROM handdraw.billing_notice_outbox n JOIN handdraw.workspaces w ON w.id=n.workspace_id JOIN handdraw.retention_episodes e USING(workspace_id,access_ended_at)
 WHERE w.lifecycle='ready' AND w.owner_user_id=n.owner_user_id AND e.status='retaining' AND e.purge_at=n.purge_at AND e.purge_at>statement_timestamp() AND NOT handdraw.entitlement_editable(w.id) AND EXISTS(SELECT 1 FROM handdraw.subscriptions sub WHERE sub.workspace_id=w.id AND sub.access_expires_at=n.access_ended_at)
 ORDER BY n.recorded_at LIMIT 100
$$;
GRANT DELETE ON handdraw.projects TO handdraw_access_owner;
CREATE POLICY access_purge_projects ON handdraw.projects FOR DELETE TO handdraw_access_owner USING(true);
ALTER TABLE handdraw.billing_change_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.billing_change_intents FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.billing_change_intents TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.billing_change_intents TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.billing_change_intents TO handdraw_access_owner;
ALTER TABLE handdraw.billing_contracts ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.billing_contracts FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.billing_contracts TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.billing_contracts TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.billing_contracts TO handdraw_access_owner;
ALTER TABLE handdraw.local_billing_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.local_billing_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.local_billing_operations TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.local_billing_operations TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.local_billing_operations TO handdraw_access_owner;
ALTER TABLE handdraw.provider_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.provider_events FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.provider_events TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.provider_events TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.provider_events TO handdraw_access_owner;
ALTER TABLE handdraw.payment_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.payment_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.payment_orders TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.payment_orders TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.payment_orders TO handdraw_access_owner;
ALTER TABLE handdraw.payment_refunds ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.payment_refunds FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.payment_refunds TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.payment_refunds TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.payment_refunds TO handdraw_access_owner;
ALTER TABLE handdraw.retention_episodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.retention_episodes FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.retention_episodes TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.retention_episodes TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.retention_episodes TO handdraw_access_owner;
ALTER TABLE handdraw.billing_notice_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.billing_notice_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_billing ON handdraw.billing_notice_outbox TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_billing ON handdraw.billing_notice_outbox TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.billing_notice_outbox TO handdraw_access_owner;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.billing_removals(text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.billing_removals(text) FROM PUBLIC;
ALTER FUNCTION handdraw.billing_access_snapshot(text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.billing_access_snapshot(text) FROM PUBLIC;
ALTER FUNCTION handdraw.billing_view(text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.billing_view(text) FROM PUBLIC;
ALTER FUNCTION handdraw.billing_request(text,text,text,uuid,jsonb) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.billing_request(text,text,text,uuid,jsonb) FROM PUBLIC;
ALTER FUNCTION handdraw.lease_billing(text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.lease_billing(text) FROM PUBLIC;
ALTER FUNCTION handdraw.local_billing_prepare(text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.local_billing_prepare(text) FROM PUBLIC;
ALTER FUNCTION handdraw.local_billing_event(text,text,text,jsonb) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.local_billing_event(text,text,text,jsonb) FROM PUBLIC;
ALTER FUNCTION handdraw.apply_billing(text,text,jsonb) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.apply_billing(text,text,jsonb) FROM PUBLIC;
ALTER FUNCTION handdraw.billing_maintenance() OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.billing_maintenance() FROM PUBLIC;
ALTER FUNCTION handdraw.local_billing_notices() OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.local_billing_notices() FROM PUBLIC;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
GRANT EXECUTE ON FUNCTION handdraw.billing_request(text,text,text,uuid,jsonb) TO handdraw_request;
GRANT EXECUTE ON FUNCTION handdraw.lease_billing(text) TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.local_billing_prepare(text) TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.local_billing_event(text,text,text,jsonb) TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.apply_billing(text,text,jsonb) TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.billing_maintenance() TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.local_billing_notices() TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.schema_compatible(bigint) TO handdraw_billing_runtime;
COMMIT;
