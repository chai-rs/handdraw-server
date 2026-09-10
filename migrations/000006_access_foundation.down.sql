BEGIN;
REVOKE UPDATE(name,revision,updated_at) ON handdraw.workspaces FROM handdraw_request;
DROP POLICY workspace_owner_rename ON handdraw.workspaces;
DROP TRIGGER workspace_metadata_guard ON handdraw.workspaces;
DROP FUNCTION handdraw.guard_workspace_metadata();
DROP FUNCTION handdraw.access_entitlement(text);
COMMIT;
