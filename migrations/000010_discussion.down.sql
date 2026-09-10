BEGIN;
DROP FUNCTION handdraw.has_personal_premium(),handdraw.edit_comment(text,text,boolean,bigint),handdraw.resolve_discussion(text,text,bigint),handdraw.reply_discussion(text,text,text),handdraw.create_discussion(text,text,text,jsonb,text),handdraw.lock_discussion(text);
DROP TABLE handdraw.comments,handdraw.comment_threads;
COMMIT;
