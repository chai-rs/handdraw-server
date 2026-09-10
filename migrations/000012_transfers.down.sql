BEGIN;
REVOKE UPDATE(state,revision,schema_version,updated_by,updated_at) ON handdraw.board_documents FROM handdraw_access_owner;
REVOKE UPDATE(status,metadata_revision,updated_at) ON handdraw.boards FROM handdraw_access_owner;
DROP POLICY read_assets ON handdraw.assets;
CREATE POLICY read_assets ON handdraw.assets FOR SELECT TO handdraw_request USING(handdraw.board_content_readable(board_id) AND (purpose='attachment' OR uploaded_by=handdraw.current_actor() OR handdraw.is_workspace_owner(workspace_id)));
DROP POLICY transfer_document_write ON handdraw.board_documents;
DROP POLICY transfer_board_write ON handdraw.boards;
CREATE OR REPLACE FUNCTION handdraw.lock_asset(a text,writing boolean) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE r handdraw.assets%ROWTYPE;
BEGIN
 SELECT * INTO r FROM handdraw.assets WHERE id=a;
 IF NOT FOUND OR NOT handdraw.board_content_readable(r.board_id) OR (r.purpose<>'attachment' AND r.uploaded_by<>handdraw.current_actor() AND NOT handdraw.is_workspace_owner(r.workspace_id)) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 PERFORM 1 FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE;
 IF NOT handdraw.board_content_readable(r.board_id) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
 IF writing AND (NOT handdraw.can_write_workspace(r.workspace_id) OR r.uploaded_by<>handdraw.current_actor()) THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
END; $$;
CREATE OR REPLACE FUNCTION handdraw.reserve_asset(a text,b text,p text,n bigint,mime text,digest text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE w text;capacity bigint;premium boolean;
BEGIN
 SELECT workspace_id INTO w FROM handdraw.boards WHERE id=b AND status='active' AND deleted_at IS NULL;
 IF w IS NULL OR NOT handdraw.lock_content_workspace(w) THEN RAISE EXCEPTION 'denied' USING ERRCODE='HD403'; END IF;
 IF NOT EXISTS(SELECT 1 FROM handdraw.boards WHERE id=b AND status='active' AND deleted_at IS NULL) THEN RAISE EXCEPTION 'not found' USING ERRCODE='HD404'; END IF;
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
 SELECT a.* INTO r FROM handdraw.assets a WHERE a.status<>'deleted' AND (NOT EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=a.board_id AND b.status<>'deleting') OR (a.expires_at<=clock_timestamp() AND (a.status='pending' OR a.purpose='import_source')) OR a.status='deleting') ORDER BY a.expires_at,a.id LIMIT 1;
 IF NOT FOUND THEN RETURN; END IF;
 -- The caller keeps this transaction open across bounded local object removal.
 PERFORM 1 FROM handdraw.workspaces WHERE id=r.workspace_id FOR UPDATE SKIP LOCKED;
 IF NOT FOUND THEN RETURN; END IF;
 SELECT * INTO r FROM handdraw.assets WHERE id=r.id AND status<>'deleted' FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 IF EXISTS(SELECT 1 FROM handdraw.boards b WHERE b.id=r.board_id AND b.status<>'deleting') AND r.status<>'deleting' AND NOT(r.expires_at<=clock_timestamp() AND (r.status='pending' OR r.purpose='import_source')) THEN RETURN; END IF;
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
 UPDATE handdraw.workspace_usage SET used_bytes=used_bytes-CASE WHEN r.accounted THEN r.size_bytes ELSE 0 END,reserved_bytes=reserved_bytes-CASE WHEN r.accounted THEN 0 ELSE r.size_bytes END,revision=revision+1,updated_at=clock_timestamp() WHERE workspace_id=r.workspace_id;
 UPDATE handdraw.assets SET status='deleted',accounted=false WHERE id=a;
END; $$;
DROP FUNCTION handdraw.fail_transfer(text,text,text),handdraw.complete_transfer(text,text,bytea),handdraw.prepare_transfer(text,text,jsonb),handdraw.transfer_context(text,text),handdraw.lease_transfer(text),handdraw.enqueue_transfer(text,text,text,text,text,text,boolean);
ALTER TABLE handdraw.assets DROP COLUMN transfer_job_id;
DROP TABLE handdraw.transfer_jobs;
DELETE FROM handdraw.assets WHERE purpose='export_artifact';
ALTER TABLE handdraw.assets DROP CONSTRAINT assets_purpose_check;
ALTER TABLE handdraw.assets ADD CHECK(purpose IN ('attachment','import_source'));
DROP FUNCTION handdraw.asset_board_readable(text);
REVOKE ALL ON SCHEMA handdraw FROM handdraw_transfer_worker;
REVOKE EXECUTE ON FUNCTION handdraw.schema_compatible(bigint) FROM handdraw_transfer_worker;
DROP ROLE handdraw_transfer_worker;
COMMIT;
