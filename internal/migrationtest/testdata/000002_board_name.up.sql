BEGIN;
ALTER TABLE handdraw_probe.boards ADD COLUMN name text NOT NULL DEFAULT 'Untitled';
COMMIT;
