BEGIN;

CREATE TABLE handdraw.polar_webhook_events (
 provider_event_id text COLLATE "C" PRIMARY KEY CHECK(length(provider_event_id) BETWEEN 1 AND 200),
 event_type text NOT NULL CHECK(event_type IN (
  'order.paid','order.refunded','refund.updated','subscription.active','subscription.canceled',
  'subscription.created','subscription.past_due','subscription.revoked','subscription.uncanceled','subscription.updated'
 )),
 occurred_at timestamptz NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','processed','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 retry_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 lease_token text,
 lease_until timestamptz,
 last_error text,
 received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 processed_at timestamptz,
 CHECK((lease_token IS NULL)=(lease_until IS NULL))
);

ALTER TABLE handdraw.polar_webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.polar_webhook_events FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_polar_webhooks ON handdraw.polar_webhook_events TO CURRENT_USER USING(true) WITH CHECK(true);

GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;

CREATE FUNCTION handdraw.accept_polar_webhook(eid text,event_name text,occurred timestamptz,body jsonb) RETURNS void
 LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE existing handdraw.polar_webhook_events%ROWTYPE;
BEGIN
 IF eid IS NULL OR length(eid) NOT BETWEEN 1 AND 200 OR occurred IS NULL OR body IS NULL OR body->>'type' IS DISTINCT FROM event_name OR body->>'timestamp' IS NULL OR jsonb_typeof(body->'data') IS DISTINCT FROM 'object' THEN
  RAISE EXCEPTION 'invalid webhook' USING ERRCODE='HD400';
 END IF;
 INSERT INTO handdraw.polar_webhook_events(provider_event_id,event_type,occurred_at,payload)
 VALUES(eid,event_name,occurred,body) ON CONFLICT(provider_event_id) DO NOTHING;
 IF NOT FOUND THEN
  SELECT * INTO existing FROM handdraw.polar_webhook_events WHERE provider_event_id=eid;
  IF existing.event_type IS DISTINCT FROM event_name OR existing.occurred_at IS DISTINCT FROM occurred OR existing.payload IS DISTINCT FROM body THEN
   RAISE EXCEPTION 'webhook identity conflict' USING ERRCODE='HD409';
  END IF;
 END IF;
 UPDATE handdraw.billing_change_intents SET retry_at=clock_timestamp()
 WHERE provider='polar' AND status IN ('requested','awaiting_provider','scheduled','applied') AND (
  id=(body#>>'{data,metadata,intent_id}') OR
  source_subscription_id=(body#>>'{data,id}') OR
  source_subscription_id=(body#>>'{data,subscription_id}') OR
  source_subscription_id=(body#>>'{data,subscription,id}') OR
  provider_checkout_id=(body#>>'{data,checkout_id}')
 );
END; $$;

CREATE FUNCTION handdraw.lease_polar_webhook(token text) RETURNS jsonb
 LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE e handdraw.polar_webhook_events%ROWTYPE;
BEGIN
 IF token!~'^[a-f0-9]{64}$' THEN RAISE EXCEPTION 'invalid lease' USING ERRCODE='HD400'; END IF;
 SELECT * INTO e FROM handdraw.polar_webhook_events
 WHERE status IN ('pending','processing') AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<=clock_timestamp())
 ORDER BY occurred_at,provider_event_id FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN NULL; END IF;
 UPDATE handdraw.polar_webhook_events SET status='processing',lease_token=token,lease_until=clock_timestamp()+interval '2 minutes',attempts=attempts+1,retry_at=clock_timestamp()+interval '30 seconds'
 WHERE provider_event_id=e.provider_event_id;
 RETURN jsonb_build_object('provider_event_id',e.provider_event_id,'type',e.event_type,'occurred_at',e.occurred_at,'payload',e.payload,'token',token,'attempts',e.attempts+1);
END; $$;

CREATE FUNCTION handdraw.complete_polar_webhook(eid text,token text,failure text) RETURNS void
 LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE e handdraw.polar_webhook_events%ROWTYPE;
BEGIN
 SELECT * INTO e FROM handdraw.polar_webhook_events WHERE provider_event_id=eid FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF e.lease_token IS DISTINCT FROM token OR e.lease_until<=clock_timestamp() THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='HD409'; END IF;
 IF failure IS NULL THEN
  UPDATE handdraw.polar_webhook_events SET status='processed',lease_token=NULL,lease_until=NULL,last_error=NULL,processed_at=clock_timestamp() WHERE provider_event_id=eid;
 ELSE
  UPDATE handdraw.polar_webhook_events SET status=CASE WHEN attempts>=10 THEN 'failed' ELSE 'pending' END,
   lease_token=NULL,lease_until=NULL,last_error=left(failure,500),retry_at=clock_timestamp()+least(interval '1 hour',interval '5 seconds'*(2^least(attempts,10)))
  WHERE provider_event_id=eid;
 END IF;
END; $$;

ALTER FUNCTION handdraw.accept_polar_webhook(text,text,timestamptz,jsonb) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.lease_polar_webhook(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.complete_polar_webhook(text,text,text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.accept_polar_webhook(text,text,timestamptz,jsonb),handdraw.lease_polar_webhook(text),handdraw.complete_polar_webhook(text,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.accept_polar_webhook(text,text,timestamptz,jsonb),handdraw.lease_polar_webhook(text),handdraw.complete_polar_webhook(text,text,text) TO handdraw_billing_runtime;
GRANT EXECUTE ON FUNCTION handdraw.schema_compatible(bigint) TO handdraw_billing_runtime;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;

COMMIT;
