BEGIN;
CREATE TABLE handdraw.comment_threads (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'thr')),
 workspace_id text COLLATE "C" NOT NULL,
 board_id text COLLATE "C" NOT NULL,
 anchor jsonb NOT NULL,
 created_by text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 status text NOT NULL DEFAULT 'open' CHECK(status IN ('open','resolved')),
 resolved_by text COLLATE "C" REFERENCES handdraw.profiles(id),
 resolved_at timestamptz,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(workspace_id,board_id,id),
 FOREIGN KEY(workspace_id,board_id) REFERENCES handdraw.boards(workspace_id,id) ON DELETE CASCADE,
 CHECK((status='resolved')=(resolved_at IS NOT NULL AND resolved_by IS NOT NULL)),
 CHECK(jsonb_typeof(anchor)='object' AND anchor->>'kind' IN ('board','page','shape','note'))
);
CREATE TABLE handdraw.comments (
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'cmt')),
 workspace_id text COLLATE "C" NOT NULL,
 board_id text COLLATE "C" NOT NULL,
 thread_id text COLLATE "C" NOT NULL,
 author_user_id text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 body text,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 deleted_at timestamptz,
 FOREIGN KEY(workspace_id,board_id,thread_id) REFERENCES handdraw.comment_threads(workspace_id,board_id,id) ON DELETE CASCADE,
 CHECK((deleted_at IS NULL AND body IS NOT NULL AND length(btrim(body)) BETWEEN 1 AND 4000) OR (deleted_at IS NOT NULL AND body IS NULL))
);
CREATE INDEX discussion_board ON handdraw.comment_threads(board_id,id);
CREATE INDEX discussion_messages ON handdraw.comments(thread_id,id);
ALTER TABLE handdraw.comment_threads ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.comment_threads FORCE ROW LEVEL SECURITY;
ALTER TABLE handdraw.comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.comments FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_threads ON handdraw.comment_threads TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY migration_comments ON handdraw.comments TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_threads ON handdraw.comment_threads TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE POLICY access_comments ON handdraw.comments TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE POLICY read_threads ON handdraw.comment_threads FOR SELECT TO handdraw_request USING(handdraw.board_content_readable(board_id));
CREATE POLICY read_comments ON handdraw.comments FOR SELECT TO handdraw_request USING(handdraw.board_content_readable(board_id));
GRANT SELECT ON handdraw.comment_threads,handdraw.comments TO handdraw_request;
GRANT SELECT,INSERT,UPDATE,DELETE ON handdraw.comment_threads,handdraw.comments TO handdraw_access_owner;
-- Readers may discuss an editable board; this lock serializes with revocation and entitlement changes.
CREATE FUNCTION handdraw.lock_discussion(b text) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;
BEGIN
 IF NOT handdraw.board_content_readable(b) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 SELECT workspace_id INTO w FROM handdraw.boards WHERE id=b;
 PERFORM 1 FROM handdraw.workspaces WHERE id=w FOR UPDATE;
 IF NOT handdraw.board_content_readable(b) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF NOT EXISTS(SELECT 1 FROM handdraw.access_entitlement(w) WHERE mode='editable') THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
 RETURN w;
END; $$;
CREATE FUNCTION handdraw.create_discussion(t text,c text,b text,a jsonb,message text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;
BEGIN
 w:=handdraw.lock_discussion(b);
 IF a - ARRAY['kind','page_id','element_id','note_id','label'] <> '{}'::jsonb OR jsonb_typeof(a)<>'object' OR coalesce(length(a->>'label'),0)>240 OR
 NOT coalesce(CASE a->>'kind'
 WHEN 'board' THEN NOT (a ?| ARRAY['page_id','element_id','note_id'])
 WHEN 'page' THEN handdraw.valid_resource_id(a->>'page_id','pag') AND NOT(a ?| ARRAY['element_id','note_id'])
 WHEN 'shape' THEN handdraw.valid_resource_id(a->>'page_id','pag') AND length(a->>'element_id') BETWEEN 1 AND 256 AND NOT(a ? 'note_id')
 WHEN 'note' THEN handdraw.valid_resource_id(a->>'note_id','note') AND NOT(a ?| ARRAY['page_id','element_id']) ELSE false END,false)
 THEN RAISE EXCEPTION 'invalid anchor' USING ERRCODE='HD400'; END IF;
 INSERT INTO handdraw.comment_threads(id,workspace_id,board_id,anchor,created_by) VALUES(t,w,b,a,handdraw.current_actor());
 INSERT INTO handdraw.comments(id,workspace_id,board_id,thread_id,author_user_id,body) VALUES(c,w,b,t,handdraw.current_actor(),message);
END; $$;
CREATE FUNCTION handdraw.reply_discussion(c text,t text,message text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.comment_threads%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.comment_threads WHERE id=t;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM handdraw.lock_discussion(r.board_id);
 INSERT INTO handdraw.comments(id,workspace_id,board_id,thread_id,author_user_id,body) VALUES(c,r.workspace_id,r.board_id,t,handdraw.current_actor(),message);
END; $$;
CREATE FUNCTION handdraw.resolve_discussion(t text,new_status text,expected bigint) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.comment_threads%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.comment_threads WHERE id=t;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM handdraw.lock_discussion(r.board_id);
 IF r.created_by<>handdraw.current_actor() AND NOT handdraw.is_workspace_owner(r.workspace_id) THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
 UPDATE handdraw.comment_threads SET status=new_status,resolved_by=CASE WHEN new_status='resolved' THEN handdraw.current_actor() END,resolved_at=CASE WHEN new_status='resolved' THEN clock_timestamp() END,revision=revision+1,updated_at=clock_timestamp() WHERE id=t AND revision=expected;
 IF NOT FOUND THEN RAISE EXCEPTION 'conflict' USING ERRCODE='HD412'; END IF;
END; $$;
CREATE FUNCTION handdraw.edit_comment(c text,message text,remove boolean,expected bigint) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.comments%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.comments WHERE id=c;
 IF NOT FOUND THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM handdraw.lock_discussion(r.board_id);
 IF r.author_user_id<>handdraw.current_actor() AND NOT(remove AND handdraw.is_workspace_owner(r.workspace_id)) THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
 UPDATE handdraw.comments SET body=CASE WHEN remove THEN NULL ELSE message END,deleted_at=CASE WHEN remove THEN clock_timestamp() END,revision=revision+1,updated_at=clock_timestamp() WHERE id=c AND revision=expected AND deleted_at IS NULL;
 IF NOT FOUND THEN RAISE EXCEPTION 'conflict' USING ERRCODE='HD412'; END IF;
END; $$;
CREATE FUNCTION handdraw.has_personal_premium() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT EXISTS(SELECT 1 FROM handdraw.workspaces w JOIN LATERAL handdraw.access_entitlement(w.id) e ON true WHERE w.owner_user_id=handdraw.current_actor() AND w.kind='personal' AND w.lifecycle='ready' AND w.deleted_at IS NULL AND e.plan='cloud' AND e.mode='editable');
$$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.lock_discussion(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.create_discussion(text,text,text,jsonb,text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.reply_discussion(text,text,text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.resolve_discussion(text,text,bigint) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.edit_comment(text,text,boolean,bigint) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.has_personal_premium() OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.lock_discussion(text),handdraw.create_discussion(text,text,text,jsonb,text),handdraw.reply_discussion(text,text,text),handdraw.resolve_discussion(text,text,bigint),handdraw.edit_comment(text,text,boolean,bigint),handdraw.has_personal_premium() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.lock_discussion(text),handdraw.create_discussion(text,text,text,jsonb,text),handdraw.reply_discussion(text,text,text),handdraw.resolve_discussion(text,text,bigint),handdraw.edit_comment(text,text,boolean,bigint),handdraw.has_personal_premium() TO handdraw_request;
COMMIT;
