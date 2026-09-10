BEGIN;
CREATE TABLE handdraw.projects (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id, 'prj')), workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200 AND name=btrim(name)), created_by text NOT NULL REFERENCES handdraw.profiles(id),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), deleted_at timestamptz, CHECK(updated_at>=created_at), UNIQUE(workspace_id,id)
);
CREATE INDEX ON handdraw.projects(workspace_id,updated_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE TABLE handdraw.boards (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id, 'brd')), workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 project_id text COLLATE "C", name text NOT NULL CHECK(char_length(name) BETWEEN 1 AND 200 AND name=btrim(name)),
 status text NOT NULL CHECK(status IN ('initializing','active','deleting')), metadata_revision bigint NOT NULL DEFAULT 1 CHECK(metadata_revision>0),
 created_by text NOT NULL REFERENCES handdraw.profiles(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), deleted_at timestamptz, CHECK(updated_at>=created_at), UNIQUE(workspace_id,id),
 FOREIGN KEY(workspace_id,project_id) REFERENCES handdraw.projects(workspace_id,id)
);
CREATE INDEX ON handdraw.boards(workspace_id,updated_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX ON handdraw.boards(workspace_id,project_id,updated_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE TABLE handdraw.board_documents (
 board_id text COLLATE "C" PRIMARY KEY, workspace_id text COLLATE "C" NOT NULL,
 state bytea NOT NULL CHECK(octet_length(state) BETWEEN 1 AND 16777216), revision bigint NOT NULL CHECK(revision>0), schema_version integer NOT NULL CHECK(schema_version>0),
 updated_by text NOT NULL REFERENCES handdraw.profiles(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), CHECK(updated_at>=created_at), FOREIGN KEY(workspace_id,board_id) REFERENCES handdraw.boards(workspace_id,id)
);
ALTER TABLE handdraw.projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.projects FORCE ROW LEVEL SECURITY;
ALTER TABLE handdraw.boards ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.boards FORCE ROW LEVEL SECURITY;
ALTER TABLE handdraw.board_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.board_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_projects ON handdraw.projects TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY migration_boards ON handdraw.boards TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY migration_documents ON handdraw.board_documents TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_projects ON handdraw.projects FOR SELECT TO handdraw_access_owner USING(true);
CREATE POLICY access_boards ON handdraw.boards FOR SELECT TO handdraw_access_owner USING(true);
CREATE POLICY access_documents ON handdraw.board_documents FOR SELECT TO handdraw_access_owner USING(true);
CREATE POLICY access_project_lock ON handdraw.projects FOR UPDATE TO handdraw_access_owner USING(true) WITH CHECK(false);
GRANT SELECT ON handdraw.projects,handdraw.boards,handdraw.board_documents TO handdraw_access_owner;
GRANT UPDATE(revision) ON handdraw.projects TO handdraw_access_owner;
CREATE POLICY project_read ON handdraw.projects FOR SELECT TO handdraw_request USING(handdraw.can_read_workspace(workspace_id));
CREATE POLICY project_insert ON handdraw.projects FOR INSERT TO handdraw_request WITH CHECK(handdraw.can_write_workspace(workspace_id) AND created_by=handdraw.current_actor() AND deleted_at IS NULL AND revision=1);
CREATE POLICY project_update ON handdraw.projects FOR UPDATE TO handdraw_request USING(handdraw.can_write_workspace(workspace_id) AND deleted_at IS NULL) WITH CHECK(handdraw.can_write_workspace(workspace_id));
CREATE POLICY board_read ON handdraw.boards FOR SELECT TO handdraw_request USING(handdraw.can_read_workspace(workspace_id) AND deleted_at IS NULL);
CREATE POLICY board_insert ON handdraw.boards FOR INSERT TO handdraw_request WITH CHECK(handdraw.can_write_workspace(workspace_id) AND created_by=handdraw.current_actor() AND deleted_at IS NULL AND metadata_revision=1 AND status IN ('active','initializing'));
CREATE POLICY board_update ON handdraw.boards FOR UPDATE TO handdraw_request USING(handdraw.can_write_workspace(workspace_id) AND deleted_at IS NULL) WITH CHECK(handdraw.can_write_workspace(workspace_id));
CREATE POLICY document_read ON handdraw.board_documents FOR SELECT TO handdraw_request USING(handdraw.can_read_workspace(workspace_id) AND EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=board_id AND b.workspace_id=board_documents.workspace_id AND (b.status='active' OR (b.status='initializing' AND b.created_by=handdraw.current_actor()))));
CREATE POLICY document_insert ON handdraw.board_documents FOR INSERT TO handdraw_request WITH CHECK(handdraw.can_write_workspace(workspace_id) AND updated_by=handdraw.current_actor() AND revision=1);
CREATE POLICY document_update ON handdraw.board_documents FOR UPDATE TO handdraw_request USING(handdraw.can_write_workspace(workspace_id) AND EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=board_id AND b.status='active')) WITH CHECK(handdraw.can_write_workspace(workspace_id) AND updated_by=handdraw.current_actor());
GRANT SELECT ON handdraw.projects,handdraw.boards,handdraw.board_documents TO handdraw_request;
GRANT INSERT(id,workspace_id,name,created_by,revision,deleted_at) ON handdraw.projects TO handdraw_request;
GRANT UPDATE(name,revision,updated_at,deleted_at) ON handdraw.projects TO handdraw_request;
GRANT INSERT(id,workspace_id,project_id,name,status,created_by,metadata_revision,deleted_at) ON handdraw.boards TO handdraw_request;
GRANT UPDATE(name,project_id,metadata_revision,status,updated_at) ON handdraw.boards TO handdraw_request;
GRANT INSERT(board_id,workspace_id,state,revision,schema_version,updated_by) ON handdraw.board_documents TO handdraw_request;
GRANT UPDATE(state,revision,schema_version,updated_by,updated_at) ON handdraw.board_documents TO handdraw_request;
CREATE FUNCTION handdraw.guard_content_row() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN
 -- Recheck after serializing against membership and entitlement changes on the parent.
 PERFORM 1 FROM handdraw.workspaces WHERE id=NEW.workspace_id FOR UPDATE;
 IF NOT EXISTS(SELECT 1 FROM pg_class WHERE oid=TG_RELID AND pg_has_role(session_user,relowner,'MEMBER'))
 AND NOT handdraw.can_write_workspace(NEW.workspace_id) THEN
  RAISE EXCEPTION 'workspace is not writable' USING ERRCODE='42501';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
   RAISE EXCEPTION 'content identity is immutable' USING ERRCODE='23514'; END IF;
  IF TG_TABLE_NAME='board_documents' THEN
   IF NEW.board_id<>OLD.board_id OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'invalid document revision or identity' USING ERRCODE='23514'; END IF;
  ELSIF NEW.id<>OLD.id OR NEW.created_by<>OLD.created_by THEN RAISE EXCEPTION 'content identity is immutable' USING ERRCODE='23514';
  ELSIF TG_TABLE_NAME='boards' THEN
   IF NEW.metadata_revision<>OLD.metadata_revision+1 OR (OLD.status='deleting' AND NEW.status<>'deleting') THEN RAISE EXCEPTION 'invalid board revision or transition' USING ERRCODE='23514'; END IF;
  ELSIF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'invalid project revision' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_TABLE_NAME='boards' THEN
 IF NEW.project_id IS NOT NULL THEN
  PERFORM 1 FROM handdraw.projects WHERE id=NEW.project_id AND workspace_id=NEW.workspace_id AND deleted_at IS NULL FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'invalid project assignment' USING ERRCODE='23514'; END IF;
  END IF;
 ELSIF TG_TABLE_NAME='projects' THEN
 IF TG_OP='UPDATE' AND NEW.deleted_at IS NOT NULL THEN
  IF EXISTS(SELECT 1 FROM handdraw.boards WHERE project_id=NEW.id AND workspace_id=NEW.workspace_id AND deleted_at IS NULL) THEN
   RAISE EXCEPTION 'project is not empty' USING ERRCODE='23514'; END IF;
 END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE FUNCTION handdraw.check_board_document() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE b text;
BEGIN
 IF TG_TABLE_NAME='boards' THEN b:=NEW.id; ELSE b:=OLD.board_id; END IF;
 IF EXISTS(SELECT 1 FROM handdraw.boards WHERE id=b) AND NOT EXISTS(SELECT 1 FROM handdraw.board_documents WHERE board_id=b) THEN
 RAISE EXCEPTION 'board requires exactly one document' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END;
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.guard_content_row() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.check_board_document() OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.guard_content_row(),handdraw.check_board_document() FROM PUBLIC;
CREATE TRIGGER project_guard BEFORE INSERT OR UPDATE ON handdraw.projects FOR EACH ROW EXECUTE FUNCTION handdraw.guard_content_row();
CREATE TRIGGER board_guard BEFORE INSERT OR UPDATE ON handdraw.boards FOR EACH ROW EXECUTE FUNCTION handdraw.guard_content_row();
CREATE TRIGGER document_guard BEFORE INSERT OR UPDATE ON handdraw.board_documents FOR EACH ROW EXECUTE FUNCTION handdraw.guard_content_row();
CREATE CONSTRAINT TRIGGER board_requires_document AFTER INSERT ON handdraw.boards DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.check_board_document();
CREATE CONSTRAINT TRIGGER document_required AFTER DELETE ON handdraw.board_documents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.check_board_document();
COMMIT;
