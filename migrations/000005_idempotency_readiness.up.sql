BEGIN;
CREATE ROLE handdraw_idempotency_gc NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT USAGE ON SCHEMA handdraw TO handdraw_idempotency_gc;
CREATE TABLE handdraw.idempotency_records (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'idem')),
 actor_user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 workspace_id text COLLATE "C" REFERENCES handdraw.workspaces(id),
 operation text NOT NULL CHECK(char_length(operation) BETWEEN 1 AND 120),
 idempotency_key uuid NOT NULL CHECK(idempotency_key<>'00000000-0000-0000-0000-000000000000'::uuid),
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 status text NOT NULL CHECK(status IN ('processing','completed')),
 result_ref jsonb, response_status integer CHECK(response_status BETWEEN 200 AND 599),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(), expires_at timestamptz NOT NULL,
 UNIQUE(actor_user_id,operation,idempotency_key), CHECK(expires_at>created_at),
 CHECK((status='completed')=(result_ref IS NOT NULL AND response_status IS NOT NULL)),
 CHECK(result_ref IS NULL OR (jsonb_typeof(result_ref)='object' AND octet_length(result_ref::text)<=4096))
);
CREATE INDEX idempotency_expiry ON handdraw.idempotency_records(expires_at);
ALTER TABLE handdraw.idempotency_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.idempotency_records FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_idempotency ON handdraw.idempotency_records TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY idempotency_read ON handdraw.idempotency_records FOR SELECT TO handdraw_request
 USING(actor_user_id=handdraw.current_actor() AND (workspace_id IS NULL OR handdraw.is_workspace_member(workspace_id)));
CREATE POLICY idempotency_insert ON handdraw.idempotency_records FOR INSERT TO handdraw_request
 WITH CHECK(actor_user_id=handdraw.current_actor() AND status='processing' AND (workspace_id IS NULL OR handdraw.is_workspace_member(workspace_id)));
CREATE POLICY idempotency_update ON handdraw.idempotency_records FOR UPDATE TO handdraw_request
 USING(actor_user_id=handdraw.current_actor() AND status='processing' AND (workspace_id IS NULL OR handdraw.is_workspace_member(workspace_id)))
 WITH CHECK(actor_user_id=handdraw.current_actor() AND status='completed' AND (workspace_id IS NULL OR handdraw.is_workspace_member(workspace_id)));
CREATE POLICY idempotency_expired_read ON handdraw.idempotency_records FOR SELECT TO handdraw_idempotency_gc USING(expires_at<=statement_timestamp());
CREATE POLICY idempotency_expired_delete ON handdraw.idempotency_records FOR DELETE TO handdraw_idempotency_gc USING(expires_at<=statement_timestamp());
GRANT SELECT ON handdraw.idempotency_records TO handdraw_request,handdraw_idempotency_gc;
GRANT DELETE ON handdraw.idempotency_records TO handdraw_idempotency_gc;
GRANT INSERT(id,actor_user_id,workspace_id,operation,idempotency_key,request_hash,status,expires_at) ON handdraw.idempotency_records TO handdraw_request;
GRANT UPDATE(status,result_ref,response_status) ON handdraw.idempotency_records TO handdraw_request;
-- Expose only compatibility, never migration credentials or unrestricted history queries.
GRANT USAGE ON SCHEMA public TO handdraw_access_owner;
GRANT SELECT ON public.handdraw_schema_migrations TO handdraw_access_owner;
CREATE FUNCTION handdraw.schema_compatible(expected bigint) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM public.handdraw_schema_migrations WHERE version=expected AND NOT dirty)
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.schema_compatible(bigint) OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.schema_compatible(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.schema_compatible(bigint) TO handdraw_identity_resolver,handdraw_request;
COMMIT;
