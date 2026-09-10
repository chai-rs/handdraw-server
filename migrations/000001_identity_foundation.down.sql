BEGIN;

DROP FUNCTION handdraw.resolve_profile(text,uuid,text);
DROP TABLE handdraw.profiles;
DROP FUNCTION handdraw.guard_profile_identity();
DROP FUNCTION handdraw.current_actor();
DROP FUNCTION handdraw.valid_resource_id(text,text);
DROP SCHEMA handdraw;
REVOKE SELECT (id), UPDATE (id) ON auth.users FROM handdraw_identity_owner;
REVOKE USAGE ON SCHEMA auth FROM handdraw_identity_owner;
DROP ROLE handdraw_identity_owner;
DROP ROLE handdraw_identity_resolver;
DROP ROLE handdraw_request;

COMMIT;
