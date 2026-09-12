BEGIN;

DROP FUNCTION handdraw.complete_polar_webhook(text,text,text);
DROP FUNCTION handdraw.lease_polar_webhook(text);
DROP FUNCTION handdraw.accept_polar_webhook(text,text,timestamptz,jsonb);
DROP TABLE handdraw.polar_webhook_events;

COMMIT;
