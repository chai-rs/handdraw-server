BEGIN;
DROP TABLE handdraw.board_documents;
DROP TABLE handdraw.boards;
DROP TABLE handdraw.projects;
DROP FUNCTION handdraw.check_board_document();
DROP FUNCTION handdraw.guard_content_row();
COMMIT;
