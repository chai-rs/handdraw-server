BEGIN;

GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;

DROP FUNCTION handdraw.apply_polar_billing(text,text,jsonb);
DROP FUNCTION handdraw.polar_portal_customer(text);

CREATE OR REPLACE FUNCTION handdraw.polar_billing_prepare(i text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.billing_change_intents%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.billing_change_intents WHERE id=i;
 IF NOT FOUND OR r.status NOT IN ('requested','awaiting_provider','scheduled','applied') THEN RAISE EXCEPTION 'invalid operation' USING ERRCODE='HD409'; END IF;
 RETURN jsonb_build_object('workspace_id',r.workspace_id,'kind',r.kind,'plan',r.target_plan,'billing_interval',r.billing_interval,'seats',r.target_seats,'checkout_id',coalesce(r.provider_checkout_id,''),'checkout_url',coalesce(r.checkout_url,''));
END; $$;
ALTER FUNCTION handdraw.polar_billing_prepare(text) OWNER TO handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.polar_billing_prepare(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.polar_billing_prepare(text) TO handdraw_billing_runtime;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;

COMMIT;
