-- Disposable pipeline fixture, not the Handdraw application schema.
BEGIN;
CREATE SCHEMA handdraw_probe;
CREATE ROLE handdraw_probe_reader NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE TABLE handdraw_probe.principals (
 id text PRIMARY KEY CHECK (id ~ '^usr_[0-9A-Za-z]{27}$')
);
CREATE TABLE handdraw_probe.boards (
 id text PRIMARY KEY,
 owner_id text NOT NULL REFERENCES handdraw_probe.principals(id) ON DELETE RESTRICT
);
CREATE INDEX boards_owner ON handdraw_probe.boards(owner_id);
ALTER TABLE handdraw_probe.principals ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw_probe.principals FORCE ROW LEVEL SECURITY;
CREATE POLICY own_principal ON handdraw_probe.principals
 FOR SELECT TO handdraw_probe_reader
 USING (id = current_setting('handdraw.user_id',true));
CREATE FUNCTION handdraw_probe.identity_guard() RETURNS trigger
 LANGUAGE plpgsql SET search_path='' AS $$
 BEGIN
  IF NEW.id <> OLD.id THEN RAISE EXCEPTION 'immutable identity'; END IF;
  RETURN NEW;
 END;
 $$;
CREATE TRIGGER immutable_identity BEFORE UPDATE ON handdraw_probe.principals
 FOR EACH ROW EXECUTE FUNCTION handdraw_probe.identity_guard();
REVOKE ALL ON FUNCTION handdraw_probe.identity_guard() FROM PUBLIC;
GRANT USAGE ON SCHEMA handdraw_probe TO handdraw_probe_reader;
GRANT SELECT ON handdraw_probe.principals TO handdraw_probe_reader;
COMMIT;
