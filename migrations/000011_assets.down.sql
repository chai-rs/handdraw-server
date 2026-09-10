BEGIN;
DROP FUNCTION handdraw.finish_asset_cleanup(text),handdraw.asset_cleanup_candidate(),handdraw.complete_asset(text),handdraw.reserve_asset(text,text,text,bigint,text,text),handdraw.lock_asset(text,boolean);
DROP TABLE handdraw.assets,handdraw.premium_catalog;
COMMIT;
