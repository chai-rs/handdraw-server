BEGIN;

ALTER TABLE handdraw.billing_change_intents
 ADD COLUMN provider text NOT NULL DEFAULT 'local' CHECK(provider IN ('local','polar')),
 ADD COLUMN provider_checkout_id text,
 ADD COLUMN checkout_url text CHECK(checkout_url IS NULL OR length(checkout_url) BETWEEN 1 AND 2083),
 ADD CONSTRAINT billing_checkout_pair CHECK((provider_checkout_id IS NULL)=(checkout_url IS NULL));

ALTER TABLE handdraw.provider_events DROP CONSTRAINT provider_events_provider_check;
ALTER TABLE handdraw.provider_events ADD CONSTRAINT provider_events_provider_check CHECK(provider IN ('local','polar'));

CREATE UNIQUE INDEX billing_provider_checkout ON handdraw.billing_change_intents(provider,provider_checkout_id) WHERE provider_checkout_id IS NOT NULL;

GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;

CREATE OR REPLACE FUNCTION handdraw.billing_view(i text) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT jsonb_build_object('id',id,'kind',kind,'target_plan',target_plan,'billing_interval',billing_interval,'target_seats',target_seats,
 'intent_revision',b.revision::text,'subscription_revision',source_revision::text,'workspace_access_revision',access_revision::text,
 'source_plan',source_plan,'current_seats',source_seats,'status',status,'phase',phase,'effective_at',effective_at,'confirmed_at',confirmed_at,'member_changes',member_changes,
 'quote',jsonb_build_object('amount_due_minor',amount_minor,'recurring_amount_minor',recurring_minor,'currency','USD','expires_at',expires_at),
 'storage',jsonb_build_object('used_bytes',u.used_bytes,'reserved_bytes',u.reserved_bytes,'target_quota_bytes',CASE WHEN target_plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*target_seats END,
 'uploads_blocked_after_change',u.used_bytes+u.reserved_bytes>CASE WHEN target_plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*target_seats END),
 'provider',provider,'checkout_url',checkout_url,'capabilities',jsonb_build_object('live_payment',provider='polar','portal',false))
 FROM handdraw.billing_change_intents b JOIN handdraw.workspace_usage u USING(workspace_id) WHERE b.id=i
$$;

CREATE FUNCTION handdraw.polar_billing_prepare(i text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i;
 IF NOT FOUND OR r.status NOT IN ('requested','awaiting_provider','scheduled','applied') THEN RAISE EXCEPTION 'invalid operation' USING ERRCODE='HD409'; END IF;
 RETURN jsonb_build_object('workspace_id',r.workspace_id,'kind',r.kind,'plan',r.target_plan,'billing_interval',r.billing_interval,'seats',r.target_seats,'checkout_id',coalesce(r.provider_checkout_id,''),'checkout_url',coalesce(r.checkout_url,''));
END; $$;

CREATE FUNCTION handdraw.apply_polar_checkout(i text,token text,checkout_id text,url text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE;
BEGIN
 IF checkout_id IS NULL OR checkout_id='' OR length(checkout_id)>200 OR url IS NULL OR length(url) NOT BETWEEN 1 AND 2083 OR url !~ '^https://' THEN RAISE EXCEPTION 'invalid checkout' USING ERRCODE='HD400'; END IF;
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF r.lease_token IS DISTINCT FROM token OR r.lease_until<=clock_timestamp() THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='HD409'; END IF;
 IF r.kind<>'checkout' THEN RAISE EXCEPTION 'unsupported operation' USING ERRCODE='HD409'; END IF;
 IF r.provider_checkout_id IS NOT NULL AND (r.provider_checkout_id<>checkout_id OR r.checkout_url<>url) THEN RAISE EXCEPTION 'checkout conflict' USING ERRCODE='HD409'; END IF;
 UPDATE handdraw.billing_change_intents SET provider='polar',provider_checkout_id=checkout_id,checkout_url=url,status='awaiting_provider',phase='awaiting_payment',lease_token=NULL,lease_until=NULL,retry_at=clock_timestamp()+interval '1 day' WHERE id=i;
END; $$;

ALTER FUNCTION handdraw.billing_view(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.polar_billing_prepare(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.apply_polar_checkout(text,text,text,text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.polar_billing_prepare(text),handdraw.apply_polar_checkout(text,text,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.polar_billing_prepare(text),handdraw.apply_polar_checkout(text,text,text,text) TO handdraw_billing_runtime;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;

COMMIT;
