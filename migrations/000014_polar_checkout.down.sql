BEGIN;

GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;

DROP FUNCTION handdraw.apply_polar_checkout(text,text,text,text);
DROP FUNCTION handdraw.polar_billing_prepare(text);
DROP INDEX handdraw.billing_provider_checkout;
ALTER TABLE handdraw.provider_events DROP CONSTRAINT provider_events_provider_check;
ALTER TABLE handdraw.provider_events ADD CONSTRAINT provider_events_provider_check CHECK(provider='local');

CREATE OR REPLACE FUNCTION handdraw.billing_view(i text) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT jsonb_build_object('id',id,'kind',kind,'target_plan',target_plan,'billing_interval',billing_interval,'target_seats',target_seats,
 'intent_revision',b.revision::text,'subscription_revision',source_revision::text,'workspace_access_revision',access_revision::text,
 'source_plan',source_plan,'current_seats',source_seats,'status',status,'phase',phase,'effective_at',effective_at,'confirmed_at',confirmed_at,'member_changes',member_changes,
 'quote',jsonb_build_object('amount_due_minor',amount_minor,'recurring_amount_minor',recurring_minor,'currency','USD','expires_at',expires_at),
 'storage',jsonb_build_object('used_bytes',u.used_bytes,'reserved_bytes',u.reserved_bytes,'target_quota_bytes',CASE WHEN target_plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*target_seats END,
 'uploads_blocked_after_change',u.used_bytes+u.reserved_bytes>CASE WHEN target_plan='cloud' THEN 5000000000::bigint ELSE 10000000000::bigint*target_seats END),
 'provider','local','checkout_url',NULL,'capabilities',jsonb_build_object('live_payment',false,'portal',false))
 FROM handdraw.billing_change_intents b JOIN handdraw.workspace_usage u USING(workspace_id) WHERE b.id=i
$$;

ALTER FUNCTION handdraw.billing_view(text) OWNER TO handdraw_access_owner;
ALTER TABLE handdraw.billing_change_intents DROP CONSTRAINT billing_checkout_pair;
ALTER TABLE handdraw.billing_change_intents DROP COLUMN checkout_url,DROP COLUMN provider_checkout_id,DROP COLUMN provider;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;

COMMIT;
