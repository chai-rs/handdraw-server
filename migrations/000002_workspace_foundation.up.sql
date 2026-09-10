BEGIN;

CREATE ROLE handdraw_access_owner NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT handdraw_access_owner TO CURRENT_USER;
GRANT USAGE ON SCHEMA handdraw TO handdraw_access_owner;
GRANT EXECUTE ON FUNCTION handdraw.current_actor(), handdraw.valid_resource_id(text,text) TO handdraw_access_owner;

CREATE TABLE handdraw.workspaces (
    id text COLLATE "C" PRIMARY KEY CHECK (handdraw.valid_resource_id(id, 'ws')),
    kind text NOT NULL CHECK (kind IN ('personal', 'team')),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200 AND btrim(name) = name),
    owner_user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
    lifecycle text NOT NULL DEFAULT 'ready' CHECK (lifecycle IN ('ready', 'purging', 'deleted')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    access_revision bigint NOT NULL DEFAULT 1 CHECK (access_revision > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    deleted_at timestamptz,
    CHECK (updated_at >= created_at),
    CHECK ((lifecycle = 'deleted') = (deleted_at IS NOT NULL)),
    CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);
CREATE INDEX workspaces_owner_kind ON handdraw.workspaces(owner_user_id, kind) WHERE deleted_at IS NULL;

CREATE TABLE handdraw.workspace_members (
    workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
    user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
    role text NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (workspace_id, user_id),
    CHECK (updated_at >= created_at)
);
CREATE UNIQUE INDEX workspace_members_one_owner ON handdraw.workspace_members(workspace_id) WHERE role = 'owner';
CREATE INDEX workspace_members_user ON handdraw.workspace_members(user_id, workspace_id);

ALTER TABLE handdraw.workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.workspaces FORCE ROW LEVEL SECURITY;
ALTER TABLE handdraw.workspace_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.workspace_members FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_workspaces ON handdraw.workspaces FOR ALL TO CURRENT_USER USING (true) WITH CHECK (true);
CREATE POLICY migration_members ON handdraw.workspace_members FOR ALL TO CURRENT_USER USING (true) WITH CHECK (true);
CREATE POLICY access_profiles ON handdraw.profiles FOR SELECT TO handdraw_access_owner USING (true);
CREATE POLICY access_workspaces ON handdraw.workspaces FOR SELECT TO handdraw_access_owner USING (true);
CREATE POLICY access_members ON handdraw.workspace_members FOR SELECT TO handdraw_access_owner USING (true);
CREATE POLICY access_revision ON handdraw.workspaces FOR UPDATE TO handdraw_access_owner USING (true) WITH CHECK (true);
GRANT SELECT (id, auth_user_id, deleted_at) ON handdraw.profiles TO handdraw_access_owner;
GRANT SELECT ON handdraw.workspaces, handdraw.workspace_members TO handdraw_access_owner;
GRANT UPDATE (access_revision, updated_at) ON handdraw.workspaces TO handdraw_access_owner;

CREATE FUNCTION handdraw.is_workspace_member(target_workspace text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
    SELECT EXISTS (
        SELECT 1 FROM handdraw.workspace_members m
        JOIN handdraw.profiles p ON p.id = m.user_id
        WHERE m.workspace_id = target_workspace AND m.user_id = handdraw.current_actor()
          AND p.auth_user_id IS NOT NULL AND p.deleted_at IS NULL
    )
$$;
CREATE FUNCTION handdraw.shares_workspace(target_user text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
    SELECT EXISTS (
        SELECT 1 FROM handdraw.workspace_members actor
        JOIN handdraw.workspace_members peer ON peer.workspace_id = actor.workspace_id
        JOIN handdraw.workspaces w ON w.id = actor.workspace_id
        JOIN handdraw.profiles p ON p.id = actor.user_id
        WHERE actor.user_id = handdraw.current_actor() AND peer.user_id = target_user
          AND w.lifecycle = 'ready' AND p.auth_user_id IS NOT NULL AND p.deleted_at IS NULL
    )
$$;

-- Updating the parent serializes membership changes and invalidates access in the same transaction.
CREATE FUNCTION handdraw.touch_membership_workspace() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
        OR NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at) THEN
        RAISE EXCEPTION 'membership identity is immutable' USING ERRCODE = '23514';
    END IF;
    UPDATE handdraw.workspaces SET access_revision = access_revision + 1, updated_at = statement_timestamp()
        WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE FUNCTION handdraw.check_workspace_owner() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE
    workspace_key text;
    workspace handdraw.workspaces%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'workspaces' THEN
        workspace_key := NEW.id;
    ELSE
        workspace_key := CASE WHEN TG_OP = 'DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END;
    END IF;
    SELECT * INTO workspace FROM handdraw.workspaces WHERE id = workspace_key;
    IF NOT FOUND THEN RETURN NULL; END IF;
    IF NOT EXISTS (SELECT 1 FROM handdraw.workspace_members m WHERE m.workspace_id = workspace_key
        AND m.user_id = workspace.owner_user_id AND m.role = 'owner') THEN
        RAISE EXCEPTION 'workspace must have its matching Owner membership' USING ERRCODE = '23514';
    END IF;
    IF workspace.kind = 'personal' AND EXISTS (SELECT 1 FROM handdraw.workspace_members m
        WHERE m.workspace_id = workspace_key AND m.user_id <> workspace.owner_user_id) THEN
        RAISE EXCEPTION 'personal workspace permits only its Owner' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

REVOKE ALL ON FUNCTION handdraw.is_workspace_member(text), handdraw.shares_workspace(text),
    handdraw.touch_membership_workspace(), handdraw.check_workspace_owner() FROM PUBLIC;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.is_workspace_member(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.shares_workspace(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.touch_membership_workspace() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.check_workspace_owner() OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
GRANT EXECUTE ON FUNCTION handdraw.is_workspace_member(text), handdraw.shares_workspace(text) TO handdraw_request;

CREATE TRIGGER membership_access_revision BEFORE INSERT OR UPDATE OR DELETE ON handdraw.workspace_members
    FOR EACH ROW EXECUTE FUNCTION handdraw.touch_membership_workspace();
CREATE CONSTRAINT TRIGGER workspace_owner_required AFTER INSERT OR UPDATE ON handdraw.workspaces
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.check_workspace_owner();
CREATE CONSTRAINT TRIGGER membership_owner_required AFTER INSERT OR UPDATE OR DELETE ON handdraw.workspace_members
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.check_workspace_owner();

CREATE POLICY workspace_member_read ON handdraw.workspaces FOR SELECT TO handdraw_request
    USING (handdraw.is_workspace_member(id));
CREATE POLICY workspace_roster_read ON handdraw.workspace_members FOR SELECT TO handdraw_request
    USING (handdraw.is_workspace_member(workspace_id));
CREATE POLICY profile_peer ON handdraw.profiles FOR SELECT TO handdraw_request
    USING (deleted_at IS NULL AND handdraw.shares_workspace(id));
GRANT SELECT ON handdraw.workspaces, handdraw.workspace_members TO handdraw_request;

-- Writes remain closed until onboarding, seat-safe membership and billing workflows exist.
COMMIT;
