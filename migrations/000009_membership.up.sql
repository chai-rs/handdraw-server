BEGIN;

CREATE TABLE handdraw.board_grants (
 workspace_id text COLLATE "C" NOT NULL,
 board_id text COLLATE "C" NOT NULL,
 user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 role text NOT NULL DEFAULT 'viewer' CHECK(role='viewer'),
 granted_by text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 expires_at timestamptz,
 PRIMARY KEY(board_id,user_id),
 FOREIGN KEY(workspace_id,board_id) REFERENCES handdraw.boards(workspace_id,id) ON DELETE CASCADE,
 CHECK(expires_at IS NULL OR expires_at>created_at)
);
CREATE INDEX board_grants_user ON handdraw.board_grants(user_id,board_id);
CREATE TABLE handdraw.invitations (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'inv')),
 workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 board_id text COLLATE "C",
 scope text NOT NULL CHECK(scope IN ('workspace','board')),
 email_normalized text NOT NULL CHECK(email_normalized=lower(btrim(email_normalized)) AND char_length(email_normalized) BETWEEN 3 AND 254),
 role text NOT NULL CHECK(role IN ('editor','viewer')),
 token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),
 invited_by text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','revoked','expired')),
 accepted_by text COLLATE "C" REFERENCES handdraw.profiles(id),
 expires_at timestamptz NOT NULL DEFAULT statement_timestamp()+interval '7 days',
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 FOREIGN KEY(workspace_id,board_id) REFERENCES handdraw.boards(workspace_id,id) ON DELETE CASCADE,
 CHECK((scope='workspace')=(board_id IS NULL)),
 CHECK(scope<>'board' OR role='viewer'),
 CHECK((status='accepted')=(accepted_by IS NOT NULL)),
 CHECK(expires_at>created_at AND updated_at>=created_at)
);
CREATE UNIQUE INDEX invitations_pending_workspace ON handdraw.invitations(workspace_id,email_normalized) WHERE status='pending' AND scope='workspace';
CREATE UNIQUE INDEX invitations_pending_board ON handdraw.invitations(board_id,email_normalized) WHERE status='pending' AND scope='board';
CREATE INDEX invitations_expiry ON handdraw.invitations(status,expires_at);
CREATE INDEX invitations_workspace_page ON handdraw.invitations(workspace_id,created_at DESC,id DESC);
ALTER TABLE handdraw.board_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.board_grants FORCE ROW LEVEL SECURITY;
ALTER TABLE handdraw.invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_grants ON handdraw.board_grants TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY migration_invitations ON handdraw.invitations TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_grants ON handdraw.board_grants TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE POLICY access_invitations ON handdraw.invitations TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.board_grants,handdraw.invitations TO handdraw_access_owner;
CREATE POLICY access_manage_members ON handdraw.workspace_members TO handdraw_access_owner USING(true) WITH CHECK(true);
GRANT INSERT,DELETE,UPDATE(role,revision,updated_at) ON handdraw.workspace_members TO handdraw_access_owner;
GRANT SELECT(display_name) ON handdraw.profiles TO handdraw_access_owner;
CREATE POLICY invitations_owner_read ON handdraw.invitations FOR SELECT TO handdraw_request USING(handdraw.is_workspace_owner(workspace_id));
CREATE POLICY grants_scoped_read ON handdraw.board_grants FOR SELECT TO handdraw_request USING(handdraw.is_workspace_owner(workspace_id) OR user_id=handdraw.current_actor());
GRANT SELECT(id,workspace_id,board_id,scope,email_normalized,role,invited_by,status,accepted_by,expires_at,created_at,updated_at) ON handdraw.invitations TO handdraw_request;
GRANT SELECT ON handdraw.board_grants TO handdraw_request;

-- Only the identity definer can compare the actor's current confirmed Auth email.
GRANT SELECT(email,email_confirmed_at) ON auth.users TO handdraw_identity_owner;
GRANT EXECUTE ON FUNCTION handdraw.current_actor() TO handdraw_identity_owner;
CREATE FUNCTION handdraw.invitation_recipient_matches(email text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM handdraw.profiles p JOIN auth.users u ON u.id=p.auth_user_id
 WHERE p.id=handdraw.current_actor() AND p.deleted_at IS NULL AND u.email_confirmed_at IS NOT NULL
 AND lower(btrim(u.email))=$1)
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_identity_owner;
ALTER FUNCTION handdraw.invitation_recipient_matches(text) OWNER TO handdraw_identity_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_identity_owner;
REVOKE ALL ON FUNCTION handdraw.invitation_recipient_matches(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.invitation_recipient_matches(text) TO handdraw_access_owner;

CREATE FUNCTION handdraw.has_board_grant(b text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM handdraw.board_grants g JOIN handdraw.profiles p ON p.id=g.user_id
 WHERE g.board_id=b AND g.user_id=handdraw.current_actor() AND p.auth_user_id IS NOT NULL AND p.deleted_at IS NULL
 AND (g.expires_at IS NULL OR g.expires_at>statement_timestamp()))
$$;
CREATE FUNCTION handdraw.board_content_readable(b text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM handdraw.boards board JOIN handdraw.workspaces w ON w.id=board.workspace_id
 JOIN handdraw.subscriptions s ON s.workspace_id=w.id
 WHERE board.id=b AND board.status='active' AND board.deleted_at IS NULL AND w.lifecycle='ready' AND w.deleted_at IS NULL
 AND (handdraw.is_workspace_member(w.id) OR handdraw.has_board_grant(b))
 AND (handdraw.entitlement_editable(w.id) OR s.access_expires_at>statement_timestamp()-interval '90 days'
 OR (s.status='active' AND s.paid_through_at>statement_timestamp()) OR (s.status='trialing' AND s.trial_ends_at>statement_timestamp())))
$$;
CREATE FUNCTION handdraw.lock_membership_workspace(w text, require_editable boolean) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN
 PERFORM 1 FROM handdraw.workspaces WHERE id=w AND lifecycle='ready' AND deleted_at IS NULL FOR UPDATE;
 IF NOT FOUND OR NOT handdraw.is_workspace_owner(w) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF require_editable AND NOT handdraw.entitlement_editable(w) THEN RAISE EXCEPTION 'not editable' USING ERRCODE='HD403'; END IF;
END;
$$;
-- Column-level UPDATE permits row locking only; the policy forbids a definer from changing billing facts.
GRANT UPDATE(workspace_id) ON handdraw.subscriptions TO handdraw_access_owner;
CREATE POLICY access_subscription_lock ON handdraw.subscriptions FOR UPDATE TO handdraw_access_owner USING(true) WITH CHECK(false);
CREATE FUNCTION handdraw.require_editor_capacity(w text, excluded_invitation text DEFAULT NULL) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE capacity integer; allocated bigint;
BEGIN
 SELECT paid_seats INTO capacity FROM handdraw.subscriptions WHERE workspace_id=w AND plan='team' FOR SHARE;
 SELECT (SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=w AND role IN ('owner','editor'))+
 (SELECT count(*) FROM handdraw.invitations WHERE workspace_id=w AND scope='workspace' AND role='editor'
 AND status='pending' AND expires_at>clock_timestamp() AND id IS DISTINCT FROM excluded_invitation) INTO allocated;
 IF capacity IS NULL OR allocated>=capacity THEN RAISE EXCEPTION 'seat capacity exhausted' USING ERRCODE='HD409'; END IF;
END;
$$;
CREATE FUNCTION handdraw.touch_sharing_workspace() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN
 UPDATE handdraw.workspaces SET access_revision=access_revision+1,updated_at=clock_timestamp()
 WHERE id=CASE WHEN TG_OP='DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER grant_access_revision BEFORE INSERT OR UPDATE OR DELETE ON handdraw.board_grants FOR EACH ROW EXECUTE FUNCTION handdraw.touch_sharing_workspace();
CREATE TRIGGER invitation_access_revision BEFORE INSERT OR UPDATE OR DELETE ON handdraw.invitations FOR EACH ROW EXECUTE FUNCTION handdraw.touch_sharing_workspace();

CREATE FUNCTION handdraw.create_invitation(candidate text,w text,b text,email text,requested_role text,digest bytea) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE existing handdraw.invitations%ROWTYPE;
BEGIN
 PERFORM handdraw.lock_membership_workspace(w,true);
 IF requested_role NOT IN ('editor','viewer') OR email<>lower(btrim(email)) OR octet_length(digest)<>32 THEN RAISE EXCEPTION 'invalid invitation' USING ERRCODE='HD400'; END IF;
 IF b IS NULL THEN
  IF NOT EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=w AND kind='team') THEN RAISE EXCEPTION 'team required' USING ERRCODE='HD403'; END IF;
 ELSE
  IF requested_role<>'viewer' OR NOT EXISTS(SELECT 1 FROM handdraw.boards WHERE id=b AND workspace_id=w AND status='active' AND deleted_at IS NULL) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 END IF;
 UPDATE handdraw.invitations SET status='expired',updated_at=clock_timestamp() WHERE workspace_id=w AND status='pending' AND expires_at<=clock_timestamp();
 SELECT * INTO existing FROM handdraw.invitations WHERE workspace_id=w AND board_id IS NOT DISTINCT FROM b AND email_normalized=email AND status='pending';
 IF FOUND THEN
  IF existing.role<>requested_role THEN RAISE EXCEPTION 'revoke before changing invitation role' USING ERRCODE='HD409'; END IF;
  RETURN existing.id;
 END IF;
 -- A confirmed existing member must use the explicit role-change workflow instead.
 IF EXISTS(SELECT 1 FROM handdraw.workspace_members m WHERE m.workspace_id=w AND handdraw.member_email_matches(m.user_id,email)) THEN
  RAISE EXCEPTION 'already a member' USING ERRCODE='HD409';
 END IF;
 IF b IS NULL AND requested_role='editor' THEN PERFORM handdraw.require_editor_capacity(w); END IF;
 INSERT INTO handdraw.invitations(id,workspace_id,board_id,scope,email_normalized,role,token_hash,invited_by)
 VALUES(candidate,w,b,CASE WHEN b IS NULL THEN 'workspace' ELSE 'board' END,email,requested_role,digest,handdraw.current_actor());
 RETURN candidate;
END;
$$;
-- This helper never exposes email and is callable only by the access definer during a scoped operation.
CREATE FUNCTION handdraw.member_email_matches(u text,email text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM handdraw.profiles p JOIN auth.users a ON a.id=p.auth_user_id
 WHERE p.id=u AND p.deleted_at IS NULL AND a.email_confirmed_at IS NOT NULL AND lower(btrim(a.email))=$2)
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_identity_owner;
ALTER FUNCTION handdraw.member_email_matches(text,text) OWNER TO handdraw_identity_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_identity_owner;
REVOKE ALL ON FUNCTION handdraw.member_email_matches(text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.member_email_matches(text,text) TO handdraw_access_owner;

CREATE FUNCTION handdraw.accept_invitation(digest bytea) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE invitation handdraw.invitations%ROWTYPE; w text; actor text:=handdraw.current_actor(); member_role text;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.invitations WHERE token_hash=digest;
 IF NOT FOUND THEN RAISE EXCEPTION 'invalid invitation' USING ERRCODE='HD404'; END IF;
 PERFORM 1 FROM handdraw.workspaces WHERE id=w AND lifecycle='ready' AND deleted_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'invalid invitation' USING ERRCODE='HD404'; END IF;
 SELECT * INTO invitation FROM handdraw.invitations WHERE token_hash=digest FOR UPDATE;
 IF NOT handdraw.invitation_recipient_matches(invitation.email_normalized) THEN RAISE EXCEPTION 'verified recipient required' USING ERRCODE='HD403'; END IF;
 IF invitation.status<>'pending' OR invitation.expires_at<=clock_timestamp() THEN RAISE EXCEPTION 'invitation no longer pending' USING ERRCODE='HD410'; END IF;
 IF NOT handdraw.entitlement_editable(w) THEN RAISE EXCEPTION 'not editable' USING ERRCODE='HD403'; END IF;
 IF NOT EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=w AND owner_user_id=invitation.invited_by) THEN RAISE EXCEPTION 'inviter changed' USING ERRCODE='HD410'; END IF;
 IF invitation.scope='workspace' THEN
  IF NOT EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=w AND kind='team') THEN RAISE EXCEPTION 'team required' USING ERRCODE='HD403'; END IF;
  SELECT role INTO member_role FROM handdraw.workspace_members WHERE workspace_id=w AND user_id=actor;
  IF FOUND THEN RAISE EXCEPTION 'already a member' USING ERRCODE='HD409'; END IF;
  IF invitation.role='editor' THEN PERFORM handdraw.require_editor_capacity(w,invitation.id); END IF;
  INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES(w,actor,invitation.role);
 ELSE
  IF NOT EXISTS(SELECT 1 FROM handdraw.boards WHERE id=invitation.board_id AND workspace_id=w AND status='active' AND deleted_at IS NULL) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
  -- Existing membership remains authoritative, without manufacturing a redundant Guest record.
  SELECT role INTO member_role FROM handdraw.workspace_members WHERE workspace_id=w AND user_id=actor;
  IF NOT FOUND THEN
   INSERT INTO handdraw.board_grants(workspace_id,board_id,user_id,role,granted_by) VALUES(w,invitation.board_id,actor,'viewer',invitation.invited_by) ON CONFLICT(board_id,user_id) DO UPDATE SET expires_at=NULL;
  END IF;
 END IF;
 UPDATE handdraw.invitations SET status='accepted',accepted_by=actor,updated_at=clock_timestamp() WHERE id=invitation.id;
 RETURN jsonb_build_object('workspace_id',w,'board_id',invitation.board_id,'user_id',actor,'role',coalesce(member_role,invitation.role),'source',CASE WHEN invitation.scope='workspace' OR member_role IS NOT NULL THEN 'workspace' ELSE 'board_grant' END);
END;
$$;
CREATE FUNCTION handdraw.revoke_invitation(invitation_id text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.invitations WHERE id=invitation_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM handdraw.lock_membership_workspace(w,false);
 UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE id=invitation_id AND status='pending';
END;
$$;
CREATE FUNCTION handdraw.change_member(w text,u text,new_role text,expected bigint) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE member handdraw.workspace_members%ROWTYPE;
BEGIN
 PERFORM handdraw.lock_membership_workspace(w,false);
 SELECT * INTO member FROM handdraw.workspace_members WHERE workspace_id=w AND user_id=u FOR UPDATE;
 IF NOT FOUND THEN
  IF new_role IS NULL THEN RETURN; END IF;
  RAISE EXCEPTION 'not found' USING ERRCODE='HD404';
 END IF;
 IF member.role='owner' THEN RAISE EXCEPTION 'owner is immutable' USING ERRCODE='HD403'; END IF;
 IF expected IS NULL OR member.revision<>expected THEN RAISE EXCEPTION 'stale member revision' USING ERRCODE='HD412'; END IF;
 IF new_role IS NULL THEN
  DELETE FROM handdraw.workspace_members WHERE workspace_id=w AND user_id=u;
 ELSE
  IF new_role NOT IN ('editor','viewer') THEN RAISE EXCEPTION 'invalid role' USING ERRCODE='HD400'; END IF;
  IF new_role='editor' AND member.role<>'editor' THEN
   IF NOT handdraw.entitlement_editable(w) THEN RAISE EXCEPTION 'not editable' USING ERRCODE='HD403'; END IF;
   PERFORM handdraw.require_editor_capacity(w);
  END IF;
  IF new_role<>member.role THEN UPDATE handdraw.workspace_members SET role=new_role,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=w AND user_id=u; END IF;
 END IF;
 -- Never allow an older pending workspace invite to undo an explicit role change or removal.
 UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE workspace_id=w AND scope='workspace' AND status='pending' AND handdraw.member_email_matches(u,email_normalized);
END;
$$;
CREATE FUNCTION handdraw.remove_board_guest(b text,u text) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text; remaining text;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.boards WHERE id=b AND status='active' AND deleted_at IS NULL;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM handdraw.lock_membership_workspace(w,false);
 DELETE FROM handdraw.board_grants WHERE board_id=b AND user_id=u;
 UPDATE handdraw.invitations SET status='revoked',updated_at=clock_timestamp() WHERE board_id=b AND status='pending' AND handdraw.member_email_matches(u,email_normalized);
 SELECT role INTO remaining FROM handdraw.workspace_members WHERE workspace_id=w AND user_id=u;
 RETURN jsonb_build_object('removed',true,'effective_access',CASE WHEN remaining IS NULL THEN NULL ELSE jsonb_build_object('role',remaining,'source','workspace') END);
END;
$$;
CREATE FUNCTION handdraw.list_board_guests(b text) RETURNS TABLE(user_id text,display_name text,role text,created_at timestamptz,expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.boards WHERE id=b AND deleted_at IS NULL;
 IF NOT FOUND OR NOT handdraw.is_workspace_owner(w) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 RETURN QUERY SELECT g.user_id,p.display_name,g.role,g.created_at,g.expires_at FROM handdraw.board_grants g JOIN handdraw.profiles p ON p.id=g.user_id WHERE g.board_id=b AND (g.expires_at IS NULL OR g.expires_at>statement_timestamp()) ORDER BY g.user_id;
END;
$$;

-- Guest facts are projected only for the exact accessible board; roster, sibling boards and workspace lists stay private.
CREATE FUNCTION handdraw.board_access_facts(b text)
RETURNS TABLE(id text,owner_user_id text,kind text,name text,lifecycle text,revision bigint,access_revision bigint,created_at timestamptz,updated_at timestamptz,user_id text,role text,member_revision bigint,plan text,mode text,grace_ends_at timestamptz,access_expires_at timestamptz,retention_ends_at timestamptz,quota_bytes bigint,used_bytes bigint,reserved_bytes bigint,usage_revision bigint,board_status text,source text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT w.id,w.owner_user_id,w.kind,w.name,w.lifecycle,w.revision,w.access_revision,w.created_at,w.updated_at,
 handdraw.current_actor(),coalesce(m.role,'viewer'),coalesce(m.revision,1),coalesce(s.plan,''),
 CASE WHEN w.lifecycle='purging' THEN 'purging' WHEN handdraw.entitlement_editable(w.id) THEN 'editable' WHEN handdraw.board_content_readable(b) THEN 'read_only' ELSE 'unavailable' END,
 s.grace_ends_at,s.access_expires_at,s.access_expires_at+interval '90 days',
 CASE WHEN s.plan='cloud' THEN 5000000000::bigint WHEN s.plan='team' THEN 10000000000::bigint*s.paid_seats ELSE 0::bigint END,
 usage.used_bytes,usage.reserved_bytes,usage.revision,board.status,CASE WHEN m.user_id IS NULL THEN 'board_grant' ELSE 'workspace' END
 FROM handdraw.boards board JOIN handdraw.workspaces w ON w.id=board.workspace_id
 LEFT JOIN handdraw.workspace_members m ON m.workspace_id=w.id AND m.user_id=handdraw.current_actor()
 LEFT JOIN handdraw.subscriptions s ON s.workspace_id=w.id JOIN handdraw.workspace_usage usage ON usage.workspace_id=w.id
 WHERE board.id=b AND board.deleted_at IS NULL AND w.deleted_at IS NULL AND
 (handdraw.is_workspace_member(w.id) OR handdraw.board_content_readable(b))
$$;

GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.has_board_grant(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.board_content_readable(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.lock_membership_workspace(text,boolean) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.require_editor_capacity(text,text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.touch_sharing_workspace() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.create_invitation(text,text,text,text,text,bytea) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.accept_invitation(bytea) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.revoke_invitation(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.change_member(text,text,text,bigint) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.remove_board_guest(text,text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.list_board_guests(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.board_access_facts(text) OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.has_board_grant(text),handdraw.board_content_readable(text),handdraw.lock_membership_workspace(text,boolean),handdraw.require_editor_capacity(text,text),handdraw.touch_sharing_workspace(),handdraw.create_invitation(text,text,text,text,text,bytea),handdraw.accept_invitation(bytea),handdraw.revoke_invitation(text),handdraw.change_member(text,text,text,bigint),handdraw.remove_board_guest(text,text),handdraw.list_board_guests(text),handdraw.board_access_facts(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.has_board_grant(text),handdraw.board_content_readable(text),handdraw.create_invitation(text,text,text,text,text,bytea),handdraw.accept_invitation(bytea),handdraw.revoke_invitation(text),handdraw.change_member(text,text,text,bigint),handdraw.remove_board_guest(text,text),handdraw.list_board_guests(text),handdraw.board_access_facts(text) TO handdraw_request;
CREATE POLICY board_guest_read ON handdraw.boards FOR SELECT TO handdraw_request USING(handdraw.board_content_readable(id));
CREATE POLICY document_guest_read ON handdraw.board_documents FOR SELECT TO handdraw_request USING(handdraw.board_content_readable(board_id));
COMMIT;
