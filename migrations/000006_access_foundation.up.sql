BEGIN;

-- Members need a public entitlement projection, never the provider identifiers on subscriptions.
CREATE FUNCTION handdraw.access_entitlement(w text)
RETURNS TABLE(plan text, mode text, grace_ends_at timestamptz, access_expires_at timestamptz, retention_ends_at timestamptz, quota_bytes bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT coalesce(s.plan,''),
 CASE WHEN ws.lifecycle='purging' THEN 'purging'
 WHEN handdraw.entitlement_editable(w) THEN 'editable'
 WHEN handdraw.can_read_workspace(w) THEN 'read_only' ELSE 'unavailable' END,
 s.grace_ends_at,s.access_expires_at,s.access_expires_at+interval '90 days',
 CASE WHEN s.plan='cloud' THEN 5000000000::bigint WHEN s.plan='team' THEN 10000000000::bigint*s.paid_seats ELSE 0::bigint END
 FROM handdraw.workspaces ws LEFT JOIN handdraw.subscriptions s ON s.workspace_id=ws.id
 WHERE ws.id=w AND ws.deleted_at IS NULL AND handdraw.is_workspace_member(w)
$$;
CREATE FUNCTION handdraw.guard_workspace_metadata() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN
 IF NEW.name IS DISTINCT FROM OLD.name OR NEW.revision IS DISTINCT FROM OLD.revision THEN
  IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'invalid workspace revision' USING ERRCODE='23514'; END IF;
  IF NOT EXISTS(SELECT 1 FROM pg_class WHERE oid=TG_RELID AND pg_has_role(session_user,relowner,'MEMBER'))
  AND NOT (handdraw.is_workspace_owner(NEW.id) AND handdraw.can_write_workspace(NEW.id)) THEN
   RAISE EXCEPTION 'workspace metadata is not writable' USING ERRCODE='42501';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.access_entitlement(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.guard_workspace_metadata() OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.access_entitlement(text),handdraw.guard_workspace_metadata() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.access_entitlement(text) TO handdraw_request;
CREATE TRIGGER workspace_metadata_guard BEFORE UPDATE ON handdraw.workspaces FOR EACH ROW EXECUTE FUNCTION handdraw.guard_workspace_metadata();
CREATE POLICY workspace_owner_rename ON handdraw.workspaces FOR UPDATE TO handdraw_request
 USING(handdraw.is_workspace_owner(id) AND handdraw.can_write_workspace(id))
 WITH CHECK(handdraw.is_workspace_owner(id) AND handdraw.can_write_workspace(id));
GRANT UPDATE(name,revision,updated_at) ON handdraw.workspaces TO handdraw_request;
COMMIT;
