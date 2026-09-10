BEGIN;
CREATE POLICY access_workspace_bootstrap ON handdraw.workspaces FOR INSERT TO handdraw_access_owner WITH CHECK(owner_user_id=handdraw.current_actor() AND lifecycle='ready' AND revision=1);
CREATE POLICY access_owner_bootstrap ON handdraw.workspace_members FOR INSERT TO handdraw_access_owner WITH CHECK(user_id=handdraw.current_actor() AND role='owner' AND EXISTS(SELECT 1 FROM handdraw.workspaces w WHERE w.id=workspace_id AND w.owner_user_id=handdraw.current_actor()));
GRANT INSERT(id,kind,name,owner_user_id) ON handdraw.workspaces TO handdraw_access_owner;
GRANT INSERT(workspace_id,user_id,role) ON handdraw.workspace_members TO handdraw_access_owner;
CREATE FUNCTION handdraw.bootstrap_workspace(w text,n text,k text) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE actor text:=handdraw.current_actor();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM handdraw.profiles WHERE id=actor AND auth_user_id IS NOT NULL AND deleted_at IS NULL) THEN
  RAISE EXCEPTION 'authenticated profile required' USING ERRCODE='42501';
 END IF;
 INSERT INTO handdraw.workspaces(id,name,kind,owner_user_id) VALUES(w,n,k,actor);
 INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES(w,actor,'owner');
 RETURN w;
END; $$;
-- Permit only an actor's expired records to be removed when its key is reused.
CREATE POLICY access_idempotency_expired ON handdraw.idempotency_records FOR SELECT TO handdraw_access_owner USING(actor_user_id=handdraw.current_actor() AND expires_at<=statement_timestamp());
CREATE POLICY access_idempotency_expired_delete ON handdraw.idempotency_records FOR DELETE TO handdraw_access_owner USING(actor_user_id=handdraw.current_actor() AND expires_at<=statement_timestamp());
GRANT SELECT,DELETE ON handdraw.idempotency_records TO handdraw_access_owner;
CREATE FUNCTION handdraw.expire_request_key(op text,k uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path='' AS $$
 DELETE FROM handdraw.idempotency_records WHERE actor_user_id=handdraw.current_actor() AND operation=op AND idempotency_key=k AND expires_at<=statement_timestamp()
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.bootstrap_workspace(text,text,text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.expire_request_key(text,uuid) OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.bootstrap_workspace(text,text,text),handdraw.expire_request_key(text,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.bootstrap_workspace(text,text,text),handdraw.expire_request_key(text,uuid) TO handdraw_request;
COMMIT;
