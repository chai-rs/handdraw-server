BEGIN;

DROP POLICY profile_peer ON handdraw.profiles;
DROP POLICY access_profiles ON handdraw.profiles;
DROP TABLE handdraw.workspace_members;
DROP TABLE handdraw.workspaces;
DROP FUNCTION handdraw.check_workspace_owner();
DROP FUNCTION handdraw.touch_membership_workspace();
DROP FUNCTION handdraw.shares_workspace(text);
DROP FUNCTION handdraw.is_workspace_member(text);
REVOKE SELECT (id, auth_user_id, deleted_at) ON handdraw.profiles FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.current_actor(), handdraw.valid_resource_id(text,text) FROM handdraw_access_owner;
REVOKE ALL ON SCHEMA handdraw FROM handdraw_access_owner;
DROP ROLE handdraw_access_owner;

COMMIT;
