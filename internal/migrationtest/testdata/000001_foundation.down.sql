BEGIN;
DROP TABLE handdraw_probe.boards;
DROP TABLE handdraw_probe.principals;
DROP FUNCTION handdraw_probe.identity_guard();
DROP SCHEMA handdraw_probe;
DROP ROLE handdraw_probe_reader;
COMMIT;
