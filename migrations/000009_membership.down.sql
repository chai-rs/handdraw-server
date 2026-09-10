BEGIN;
DROP POLICY document_guest_read ON handdraw.board_documents;
DROP POLICY board_guest_read ON handdraw.boards;
DROP FUNCTION handdraw.board_access_facts(text),handdraw.list_board_guests(text),handdraw.remove_board_guest(text,text),handdraw.change_member(text,text,text,bigint),handdraw.revoke_invitation(text),handdraw.accept_invitation(bytea),handdraw.create_invitation(text,text,text,text,text,bytea),handdraw.require_editor_capacity(text,text),handdraw.lock_membership_workspace(text,boolean);
DROP FUNCTION handdraw.board_content_readable(text),handdraw.has_board_grant(text);
DROP TABLE handdraw.invitations,handdraw.board_grants;
DROP FUNCTION handdraw.touch_sharing_workspace(),handdraw.member_email_matches(text,text),handdraw.invitation_recipient_matches(text);
DROP POLICY access_subscription_lock ON handdraw.subscriptions;
REVOKE UPDATE(workspace_id) ON handdraw.subscriptions FROM handdraw_access_owner;
DROP POLICY access_manage_members ON handdraw.workspace_members;
REVOKE DELETE,UPDATE(role,revision,updated_at) ON handdraw.workspace_members FROM handdraw_access_owner;
-- INSERT(workspace_id,user_id,role) remains needed by the T04 bootstrap definer.
REVOKE INSERT ON handdraw.workspace_members FROM handdraw_access_owner;
GRANT INSERT(workspace_id,user_id,role) ON handdraw.workspace_members TO handdraw_access_owner;
REVOKE SELECT(display_name) ON handdraw.profiles FROM handdraw_access_owner;
REVOKE SELECT(email,email_confirmed_at) ON auth.users FROM handdraw_identity_owner;
REVOKE EXECUTE ON FUNCTION handdraw.current_actor() FROM handdraw_identity_owner;
COMMIT;
