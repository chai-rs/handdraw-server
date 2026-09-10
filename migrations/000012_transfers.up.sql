BEGIN;
CREATE ROLE handdraw_transfer_worker NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT USAGE ON SCHEMA handdraw TO handdraw_transfer_worker;
ALTER TABLE handdraw.assets DROP CONSTRAINT assets_purpose_check;
ALTER TABLE handdraw.assets ADD CHECK(purpose IN ('attachment','import_source','export_artifact'));
CREATE TABLE handdraw.transfer_jobs(
 id text COLLATE "C" PRIMARY KEY CHECK(handdraw.valid_resource_id(id,'job')),
 workspace_id text COLLATE "C" NOT NULL REFERENCES handdraw.workspaces(id),
 board_id text COLLATE "C" NOT NULL,
 actor text COLLATE "C" NOT NULL REFERENCES handdraw.profiles(id),
 kind text NOT NULL CHECK(kind IN ('import','export')),
 format text NOT NULL CHECK(format IN ('handdraw','excalidraw')),
 source_asset_id text COLLATE "C" REFERENCES handdraw.assets(id),
 page_id text NOT NULL DEFAULT '',include_assets boolean NOT NULL DEFAULT true,
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token text,lease_until timestamptz,
 input_state bytea CHECK(octet_length(input_state)<=16777216),
 result_asset_id text COLLATE "C" REFERENCES handdraw.assets(id),
 error_code text,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),completed_at timestamptz,
 CHECK((status='running')=(lease_token IS NOT NULL AND lease_until IS NOT NULL))
);
ALTER TABLE handdraw.assets ADD COLUMN transfer_job_id text COLLATE "C" REFERENCES handdraw.transfer_jobs(id);
CREATE INDEX eligible_transfers ON handdraw.transfer_jobs(status,retry_at);
CREATE INDEX transferred_assets ON handdraw.assets(transfer_job_id);
ALTER TABLE handdraw.transfer_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.transfer_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_transfers ON handdraw.transfer_jobs TO CURRENT_USER USING(true) WITH CHECK(true);
CREATE POLICY access_transfers ON handdraw.transfer_jobs TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE FUNCTION handdraw.asset_board_readable(b text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path='' AS $$
 SELECT handdraw.board_content_readable(b) OR EXISTS(SELECT 1 FROM handdraw.boards WHERE id=b AND status='initializing' AND deleted_at IS NULL AND created_by=handdraw.current_actor() AND handdraw.can_write_workspace(workspace_id));
$$;
CREATE POLICY read_transfers ON handdraw.transfer_jobs FOR SELECT TO handdraw_request USING((actor=handdraw.current_actor() OR handdraw.is_workspace_owner(workspace_id)) AND handdraw.asset_board_readable(board_id));
GRANT SELECT(id,board_id,kind,status,attempts,result_asset_id,error_code) ON handdraw.transfer_jobs TO handdraw_request;
GRANT SELECT,INSERT,UPDATE ON handdraw.transfer_jobs TO handdraw_access_owner;
GRANT UPDATE(state,revision,schema_version,updated_by,updated_at) ON handdraw.board_documents TO handdraw_access_owner;
GRANT UPDATE(status,metadata_revision,updated_at) ON handdraw.boards TO handdraw_access_owner;
CREATE POLICY transfer_document_write ON handdraw.board_documents FOR UPDATE TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE POLICY transfer_board_write ON handdraw.boards FOR UPDATE TO handdraw_access_owner USING(true) WITH CHECK(true);
CREATE OR REPLACE FUNCTION handdraw.lock_asset(a text,writing boolean) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.assets%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.assets WHERE id=a;
 IF NOT FOUND OR NOT handdraw.asset_board_readable(r.board_id) OR (r.purpose<>'attachment' AND r.uploaded_by<>handdraw.current_actor() AND NOT handdraw.is_workspace_owner(r.workspace_id)) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM 1 FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE;
 IF NOT handdraw.asset_board_readable(r.board_id) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF writing AND (NOT handdraw.can_write_workspace(r.workspace_id) OR r.uploaded_by<>handdraw.current_actor() OR r.purpose='export_artifact') THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
END; $$;
DROP POLICY read_assets ON handdraw.assets;
CREATE POLICY read_assets ON handdraw.assets FOR SELECT TO handdraw_request USING(handdraw.asset_board_readable(board_id) AND (purpose='attachment' OR uploaded_by=handdraw.current_actor() OR handdraw.is_workspace_owner(workspace_id)));
CREATE FUNCTION handdraw.enqueue_transfer(j text,b text,k text,f text,source_id text,page text,attachments boolean) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text; snapshot bytea;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.boards WHERE id=b AND deleted_at IS NULL;
 PERFORM 1 FROM handdraw.workspaces WHERE id=w FOR UPDATE;
 IF k='import' THEN
  IF NOT handdraw.can_write_workspace(w) OR NOT EXISTS(SELECT 1 FROM handdraw.boards WHERE id=b AND status='initializing' AND created_by=handdraw.current_actor()) OR NOT EXISTS(SELECT 1 FROM handdraw.assets WHERE id=source_id AND board_id=b AND purpose='import_source' AND status='available' AND uploaded_by=handdraw.current_actor() AND expires_at>clock_timestamp()) THEN RAISE EXCEPTION 'import denied' USING ERRCODE='HD403'; END IF;
  IF EXISTS(SELECT 1 FROM handdraw.transfer_jobs WHERE board_id=b AND kind='import' AND status<>'failed') THEN RAISE EXCEPTION 'import already exists' USING ERRCODE='HD409'; END IF;
 ELSE
  IF NOT handdraw.board_content_readable(b) THEN RAISE EXCEPTION 'export denied' USING ERRCODE='HD403'; END IF;
  SELECT state INTO snapshot FROM handdraw.board_documents WHERE board_id=b;
 END IF;
 IF (SELECT count(*) FROM handdraw.transfer_jobs WHERE actor=handdraw.current_actor() AND status IN ('queued','running'))>=3 THEN RAISE EXCEPTION 'too many jobs' USING ERRCODE='HD409'; END IF;
 INSERT INTO handdraw.transfer_jobs(id,workspace_id,board_id,actor,kind,format,source_asset_id,page_id,include_assets,input_state) VALUES(j,w,b,handdraw.current_actor(),k,f,nullif(source_id,''),page,attachments,snapshot);
END; $$;
CREATE FUNCTION handdraw.lease_transfer(token text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE j handdraw.transfer_jobs%ROWTYPE;
BEGIN
 IF length(token)<>64 THEN RAISE EXCEPTION 'invalid lease' USING ERRCODE='HD400'; END IF;
 PERFORM pg_advisory_xact_lock(724104182612);
 UPDATE handdraw.transfer_jobs SET status=CASE WHEN attempts>=5 OR created_at<clock_timestamp()-interval '1 day' THEN 'failed' ELSE 'queued' END,lease_token=NULL,lease_until=NULL,error_code='lease_expired',input_state=CASE WHEN attempts>=5 OR created_at<clock_timestamp()-interval '1 day' THEN NULL ELSE input_state END WHERE status='running' AND lease_until<=clock_timestamp();
 UPDATE handdraw.transfer_jobs SET status='failed',error_code='job_expired',input_state=NULL,completed_at=clock_timestamp() WHERE status='queued' AND created_at<clock_timestamp()-interval '1 day';
 IF (SELECT count(*) FROM handdraw.transfer_jobs WHERE status='running')>=2 THEN RETURN NULL; END IF;
 SELECT * INTO j FROM handdraw.transfer_jobs WHERE status='queued' AND retry_at<=clock_timestamp() ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN NULL; END IF;
 UPDATE handdraw.transfer_jobs SET status='running',attempts=attempts+1,lease_token=token,lease_until=clock_timestamp()+interval '2 minutes' WHERE id=j.id;
 RETURN jsonb_build_object('id',j.id,'actor',j.actor,'token',token);
END; $$;
CREATE FUNCTION handdraw.transfer_context(job text,token text) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE j handdraw.transfer_jobs%ROWTYPE;premium boolean;
BEGIN
 SELECT * INTO j FROM handdraw.transfer_jobs WHERE id=job;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing job' USING ERRCODE='HD404'; END IF;
 PERFORM 1 FROM handdraw.workspaces WHERE id=j.workspace_id FOR UPDATE;
 SELECT * INTO j FROM handdraw.transfer_jobs WHERE id=job FOR UPDATE;
 IF j.status<>'running' OR j.lease_token<>token OR j.lease_until<=clock_timestamp() THEN RAISE EXCEPTION 'lease conflict' USING ERRCODE='HD409'; END IF;
 PERFORM set_config('handdraw.user_id',j.actor,true);
 IF NOT EXISTS(SELECT 1 FROM handdraw.profiles WHERE id=j.actor AND auth_user_id IS NOT NULL AND deleted_at IS NULL) THEN RAISE EXCEPTION 'revoked actor' USING ERRCODE='HD403'; END IF;
 IF (j.kind='import' AND (NOT handdraw.can_write_workspace(j.workspace_id) OR NOT EXISTS(SELECT 1 FROM handdraw.boards WHERE id=j.board_id AND status='initializing' AND created_by=j.actor AND deleted_at IS NULL))) OR (j.kind='export' AND NOT handdraw.board_content_readable(j.board_id)) THEN RAISE EXCEPTION 'access changed' USING ERRCODE='HD403'; END IF;
 premium:=handdraw.has_personal_premium() OR EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=j.workspace_id AND kind='team' AND handdraw.can_write_workspace(j.workspace_id));
 RETURN jsonb_build_object('id',j.id,'board_id',j.board_id,'workspace_id',j.workspace_id,'actor',j.actor,'kind',j.kind,'format',j.format,'page_id',j.page_id,'include_assets',j.include_assets,'state',encode(j.input_state,'base64'),'created_at',j.created_at,'premium',premium,
 'source',(SELECT to_jsonb(a)-'accounted'-'created_at'-'transfer_job_id' FROM handdraw.assets a WHERE a.id=j.source_asset_id AND a.status='available' AND a.expires_at>clock_timestamp()),
 'assets',coalesce((SELECT jsonb_agg(to_jsonb(a)-'accounted'-'created_at'-'transfer_job_id') FROM handdraw.assets a WHERE a.board_id=j.board_id AND a.purpose='attachment' AND a.status='available' AND a.expires_at>clock_timestamp()),'[]'::jsonb),
 'prepared',coalesce((SELECT jsonb_agg(to_jsonb(a)-'accounted'-'created_at'-'transfer_job_id' ORDER BY a.id) FROM handdraw.assets a WHERE a.transfer_job_id=j.id AND a.status='pending'),'[]'::jsonb));
END; $$;
CREATE FUNCTION handdraw.prepare_transfer(job text,token text,manifest jsonb) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE j handdraw.transfer_jobs%ROWTYPE;r jsonb;capacity bigint;total bigint;premium boolean;
BEGIN
 PERFORM handdraw.transfer_context(job,token);
 SELECT * INTO j FROM handdraw.transfer_jobs WHERE id=job;
 IF jsonb_typeof(manifest)<>'array' OR jsonb_array_length(manifest)>100 THEN RAISE EXCEPTION 'manifest invalid' USING ERRCODE='HD400'; END IF;
 FOR r IN SELECT * FROM jsonb_array_elements(manifest) LOOP
  IF NOT handdraw.valid_resource_id(r->>'id','ast') OR (r->>'size_bytes')::bigint NOT BETWEEN 1 AND (CASE WHEN j.kind='export' THEN 64000000 ELSE 20000000 END) OR r->>'sha256' !~ '^[0-9a-f]{64}$' THEN RAISE EXCEPTION 'asset invalid' USING ERRCODE='HD400'; END IF;
  IF EXISTS(SELECT 1 FROM handdraw.assets WHERE id=r->>'id') THEN
   IF NOT EXISTS(SELECT 1 FROM handdraw.assets WHERE id=r->>'id' AND transfer_job_id=j.id AND sha256=r->>'sha256' AND size_bytes=(r->>'size_bytes')::bigint AND mime_type=r->>'mime_type' AND status='pending') THEN RAISE EXCEPTION 'manifest conflict' USING ERRCODE='HD409'; END IF;
   CONTINUE;
  END IF;
  SELECT EXISTS(SELECT 1 FROM handdraw.premium_catalog WHERE sha256=r->>'sha256') OR coalesce((r->>'premium')::boolean,false) INTO premium;
  IF j.kind='import' THEN
   IF premium AND NOT(handdraw.has_personal_premium() OR EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=j.workspace_id AND kind='team')) THEN RAISE EXCEPTION 'premium denied' USING ERRCODE='HD403'; END IF;
   SELECT quota_bytes INTO capacity FROM handdraw.access_entitlement(j.workspace_id);
   UPDATE handdraw.workspace_usage SET reserved_bytes=reserved_bytes+(r->>'size_bytes')::bigint,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=j.workspace_id AND used_bytes+reserved_bytes+(r->>'size_bytes')::bigint<=capacity;
   IF NOT FOUND THEN RAISE EXCEPTION 'quota exceeded' USING ERRCODE='HD413'; END IF;
  ELSE
   SELECT coalesce(sum(size_bytes),0) INTO total FROM handdraw.assets WHERE workspace_id=j.workspace_id AND purpose='export_artifact' AND status<>'deleted';
   IF total+(r->>'size_bytes')::bigint>256000000 THEN RAISE EXCEPTION 'export budget' USING ERRCODE='HD413'; END IF;
  END IF;
  INSERT INTO handdraw.assets(id,workspace_id,board_id,uploaded_by,purpose,size_bytes,mime_type,sha256,premium,transfer_job_id,expires_at) VALUES(r->>'id',j.workspace_id,j.board_id,j.actor,CASE WHEN j.kind='export' THEN 'export_artifact' ELSE 'attachment' END,(r->>'size_bytes')::bigint,r->>'mime_type',r->>'sha256',premium,j.id,clock_timestamp()+CASE WHEN j.kind='export' THEN interval '1 hour' ELSE interval '15 minutes' END);
 END LOOP;
END; $$;
CREATE FUNCTION handdraw.complete_transfer(job text,token text,state bytea) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE j handdraw.transfer_jobs%ROWTYPE;total bigint;artifact text;
BEGIN
 PERFORM handdraw.transfer_context(job,token);SELECT * INTO j FROM handdraw.transfer_jobs WHERE id=job;
 IF EXISTS(SELECT 1 FROM handdraw.assets WHERE transfer_job_id=j.id AND (status<>'pending' OR expires_at<=clock_timestamp())) THEN RAISE EXCEPTION 'asset expired' USING ERRCODE='HD409'; END IF;
 IF j.kind='import' THEN
  IF state IS NULL OR octet_length(state) NOT BETWEEN 1 AND 16777216 THEN RAISE EXCEPTION 'document invalid' USING ERRCODE='HD400'; END IF;
  SELECT coalesce(sum(size_bytes),0) INTO total FROM handdraw.assets WHERE transfer_job_id=j.id;
  UPDATE handdraw.workspace_usage SET reserved_bytes=reserved_bytes-total,used_bytes=used_bytes+total,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=j.workspace_id;
  UPDATE handdraw.board_documents SET state=complete_transfer.state,revision=revision+1,updated_by=j.actor,updated_at=clock_timestamp() WHERE board_id=j.board_id;
  UPDATE handdraw.boards SET status='active',metadata_revision=metadata_revision+1,updated_at=clock_timestamp() WHERE id=j.board_id;
  UPDATE handdraw.assets SET status='deleting' WHERE id=j.source_asset_id;
 ELSE
  SELECT id INTO artifact FROM handdraw.assets WHERE transfer_job_id=j.id;
  IF artifact IS NULL OR (SELECT count(*) FROM handdraw.assets WHERE transfer_job_id=j.id)<>1 THEN RAISE EXCEPTION 'artifact missing' USING ERRCODE='HD409'; END IF;
 END IF;
 UPDATE handdraw.assets SET status='available',accounted=(j.kind='import') WHERE transfer_job_id=j.id;
 UPDATE handdraw.transfer_jobs SET status='succeeded',lease_token=NULL,lease_until=NULL,result_asset_id=artifact,input_state=NULL,completed_at=clock_timestamp(),error_code=NULL WHERE id=j.id;
END; $$;
CREATE FUNCTION handdraw.fail_transfer(job text,token text,code text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
BEGIN
 UPDATE handdraw.transfer_jobs SET status=CASE WHEN attempts>=5 OR code IN ('invalid_transfer','permission_denied') THEN 'failed' ELSE 'queued' END,lease_token=NULL,lease_until=NULL,retry_at=clock_timestamp()+interval '5 seconds'*power(2,attempts),error_code=code,input_state=CASE WHEN attempts>=5 OR code IN ('invalid_transfer','permission_denied') THEN NULL ELSE input_state END,completed_at=CASE WHEN attempts>=5 OR code IN ('invalid_transfer','permission_denied') THEN clock_timestamp() ELSE NULL END WHERE id=job AND status='running' AND lease_token=token;
END; $$;
GRANT CREATE ON SCHEMA handdraw TO handdraw_access_owner;
ALTER FUNCTION handdraw.asset_board_readable(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.enqueue_transfer(text,text,text,text,text,text,boolean) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.lease_transfer(text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.transfer_context(text,text) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.prepare_transfer(text,text,jsonb) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.complete_transfer(text,text,bytea) OWNER TO handdraw_access_owner;
ALTER FUNCTION handdraw.fail_transfer(text,text,text) OWNER TO handdraw_access_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_access_owner;
REVOKE ALL ON FUNCTION handdraw.asset_board_readable(text),handdraw.enqueue_transfer(text,text,text,text,text,text,boolean),handdraw.lease_transfer(text),handdraw.transfer_context(text,text),handdraw.prepare_transfer(text,text,jsonb),handdraw.complete_transfer(text,text,bytea),handdraw.fail_transfer(text,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.asset_board_readable(text),handdraw.enqueue_transfer(text,text,text,text,text,text,boolean) TO handdraw_request;
GRANT EXECUTE ON FUNCTION handdraw.lease_transfer(text),handdraw.transfer_context(text,text),handdraw.prepare_transfer(text,text,jsonb),handdraw.complete_transfer(text,text,bytea),handdraw.fail_transfer(text,text,text),handdraw.schema_compatible(bigint) TO handdraw_transfer_worker;
CREATE OR REPLACE FUNCTION handdraw.reserve_asset(a text,b text,p text,n bigint,mime text,digest text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;capacity bigint;premium boolean;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.boards WHERE id=b AND (status='active' OR (status='initializing' AND created_by=handdraw.current_actor())) AND deleted_at IS NULL;
 IF w IS NULL OR NOT handdraw.lock_content_workspace(w) THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
 IF NOT EXISTS(SELECT 1 FROM handdraw.boards WHERE id=b AND (status='active' OR (status='initializing' AND created_by=handdraw.current_actor())) AND deleted_at IS NULL) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 SELECT EXISTS(SELECT 1 FROM handdraw.premium_catalog c WHERE c.sha256=digest) INTO premium;
 IF premium AND NOT(handdraw.has_personal_premium() OR EXISTS(SELECT 1 FROM handdraw.workspaces WHERE id=w AND kind='team')) THEN RAISE EXCEPTION 'premium denied' USING ERRCODE='HD403'; END IF;
 SELECT quota_bytes INTO capacity FROM handdraw.access_entitlement(w);
 UPDATE handdraw.workspace_usage SET reserved_bytes=reserved_bytes+n,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=w AND n>0 AND used_bytes+reserved_bytes+n<=capacity;
 IF NOT FOUND THEN RAISE EXCEPTION 'quota exceeded' USING ERRCODE='HD413'; END IF;
 INSERT INTO handdraw.assets(id,workspace_id,board_id,uploaded_by,purpose,size_bytes,mime_type,sha256,premium) VALUES(a,w,b,handdraw.current_actor(),p,n,mime,digest,premium);
END; $$;
CREATE OR REPLACE FUNCTION handdraw.asset_cleanup_candidate() RETURNS SETOF handdraw.assets LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.assets%ROWTYPE;
BEGIN
 SELECT a.* INTO r FROM handdraw.assets a WHERE a.status<>'deleted' AND (NOT EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=a.board_id AND b.status<>'deleting') OR (a.expires_at<=clock_timestamp() AND (a.status='pending' OR a.purpose IN ('import_source','export_artifact'))) OR a.status='deleting') ORDER BY a.expires_at,a.id LIMIT 1;
 IF NOT FOUND THEN RETURN; END IF;
 -- The caller keeps this transaction open across bounded local object removal.
 PERFORM 1 FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE SKIP LOCKED;
 IF NOT FOUND THEN RETURN; END IF;
 SELECT * INTO r FROM handdraw.assets WHERE id=r.id AND status<>'deleted' FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 IF EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=r.board_id AND b.status<>'deleting') AND r.status<>'deleting' AND NOT(r.expires_at<=clock_timestamp() AND (r.status='pending' OR r.purpose IN ('import_source','export_artifact'))) THEN RETURN; END IF;
 UPDATE handdraw.assets SET status='deleting' WHERE id=r.id;
 RETURN NEXT r;
END; $$;
CREATE OR REPLACE FUNCTION handdraw.finish_asset_cleanup(a text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.assets%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.assets WHERE id=a;
 IF NOT FOUND OR r.status='deleted' THEN RETURN; END IF;
 PERFORM 1 FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE;
 SELECT * INTO r FROM handdraw.assets WHERE id=a FOR UPDATE;
 IF r.status<>'deleting' THEN RAISE EXCEPTION 'invalid cleanup' USING ERRCODE='HD409'; END IF;
 UPDATE handdraw.workspace_usage SET used_bytes=used_bytes-CASE WHEN r.accounted THEN r.size_bytes ELSE 0 END,reserved_bytes=reserved_bytes-CASE WHEN r.accounted OR r.purpose='export_artifact' THEN 0 ELSE r.size_bytes END,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id;
 UPDATE handdraw.assets SET status='deleted',accounted=false WHERE id=a;
END; $$;
COMMIT;
