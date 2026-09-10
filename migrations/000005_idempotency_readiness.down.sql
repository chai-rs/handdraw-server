BEGIN;
DROP FUNCTION handdraw.schema_compatible(bigint);
REVOKE SELECT ON public.handdraw_schema_migrations FROM handdraw_access_owner;
REVOKE USAGE ON SCHEMA public FROM handdraw_access_owner;
DROP TABLE handdraw.idempotency_records;
REVOKE USAGE ON SCHEMA handdraw FROM handdraw_idempotency_gc;
DROP ROLE handdraw_idempotency_gc;
COMMIT;
