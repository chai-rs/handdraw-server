BEGIN;
CREATE ROLE handdraw_cleanup_worker NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT USAGE ON SCHEMA handdraw TO handdraw_cleanup_worker;
CREATE TABLE handdraw.board_deletion_jobs(
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'job')),
 board_id text COLLATE "C" NOT NULL UNIQUE CHECK(handdraw.valid_resource_id(board_id,'brd')),
 workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','succeeded','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 CHECK((status='succeeded')=(completed_at IS NOT NULL))
);
ALTER TABLE handdraw.board_deletion_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.board_deletion_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_cleanup ON handdraw.board_deletion_jobs TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_cleanup ON handdraw.board_deletion_jobs TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE POLICY request_cleanup_read ON handdraw.board_deletion_jobs FOR SELECT TO handdraw_request USING(handdraw.is_workspace_member(workspace_id));
CREATE POLICY request_cleanup_insert ON handdraw.board_deletion_jobs FOR INSERT TO handdraw_request WITH CHECK(status='queued' AND attempts=0 AND completed_at IS NULL AND handdraw.can_write_workspace(workspace_id) AND EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=board_id AND b.workspace_id=board_deletion_jobs.workspace_id AND b.status='deleting'));
GRANT SELECT ON handdraw.board_deletion_jobs TO handdraw_request;
GRANT INSERT(id,board_id,workspace_id) ON handdraw.board_deletion_jobs TO handdraw_request;
GRANT SELECT,UPDATE ON handdraw.board_deletion_jobs TO handdraw_access_owner;
GRANT DELETE ON handdraw.board_documents,handdraw.boards TO handdraw_access_owner;
CREATE POLICY access_cleanup_documents ON handdraw.board_documents FOR DELETE TO handdraw_access_owner USING(true);
CREATE POLICY access_cleanup_boards ON handdraw.boards FOR DELETE TO handdraw_access_owner USING(true);
-- All app mutations take the workspace before project/board locks, matching entitlement changes.
CREATE FUNCTION handdraw.lock_content_workspace(w text) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN
 IF NOT handdraw.can_write_workspace(w) THEN RETURN false; END IF;
 PERFORM 1 FROM handdraw.workspaces WHERE id=w FOR UPDATE;
 RETURN handdraw.can_write_workspace(w);
END; $$;
-- One transaction owns the row lock and all current cleanup; a crash releases it without losing the queued job.
CREATE FUNCTION handdraw.run_board_cleanup() RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE j handdraw.board_deletion_jobs%ROWTYPE;
BEGIN
 SELECT * INTO j FROM handdraw.board_deletion_jobs WHERE status='queued' AND retry_at<=clock_timestamp() ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN 0; END IF;
 BEGIN
  PERFORM 1 FROM handdraw.workspaces WHERE id=j.workspace_id FOR UPDATE;
  IF EXISTS(SELECT 1 FROM handdraw.boards WHERE id=j.board_id AND (workspace_id<>j.workspace_id OR status<>'deleting')) THEN RAISE EXCEPTION 'cleanup target is not deleting'; END IF;
  DELETE FROM handdraw.board_documents WHERE board_id=j.board_id AND workspace_id=j.workspace_id;
  DELETE FROM handdraw.boards WHERE id=j.board_id AND workspace_id=j.workspace_id AND status='deleting';
  UPDATE handdraw.board_deletion_jobs SET status='succeeded',attempts=attempts+1,completed_at=clock_timestamp() WHERE id=j.id;
 EXCEPTION WHEN OTHERS THEN
  UPDATE handdraw.board_deletion_jobs SET attempts=attempts+1,status=CASE WHEN attempts+1>=5 THEN 'failed' ELSE 'queued' END,retry_at=clock_timestamp()+interval '5 seconds' WHERE id=j.id;
 END;
 RETURN 1;
END; $$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.run_board_cleanup() OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.lock_content_workspace(text) OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.run_board_cleanup(),handdraw.lock_content_workspace(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.run_board_cleanup(),handdraw.schema_compatible(bigint) TO handdraw_cleanup_worker;
GRANT EXECUTE ON FUNCTION handdraw.lock_content_workspace(text) TO handdraw_request;
COMMIT;
