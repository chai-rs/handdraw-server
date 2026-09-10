BEGIN;

-- Login credentials are provisioned separately; runtime logins inherit only their capability role.
CREATE ROLE handdraw_request NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE ROLE handdraw_identity_resolver NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE ROLE handdraw_identity_owner NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT handdraw_identity_owner TO CURRENT_USER;

CREATE SCHEMA handdraw;
REVOKE ALL ON SCHEMA handdraw FROM PUBLIC;
GRANT USAGE ON SCHEMA handdraw TO handdraw_request, handdraw_identity_resolver, handdraw_identity_owner;

-- C collation gives the same base62 ordering as a canonical, nonzero 160-bit KSUID.
CREATE FUNCTION handdraw.valid_resource_id(value text, prefix text) RETURNS boolean
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE SET search_path = '' AS $$
    SELECT value ~ ('^' || prefix || '_[0-9A-Za-z]{27}$')
       AND substring(value FROM char_length(prefix) + 2) COLLATE "C"
           BETWEEN '000000000000000000000000001' AND 'aWgEPTl1tmebfsQzFP4bxwgy80V'
$$;
REVOKE ALL ON FUNCTION handdraw.valid_resource_id(text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.valid_resource_id(text,text) TO handdraw_request, handdraw_identity_owner;

CREATE FUNCTION handdraw.current_actor() RETURNS text
LANGUAGE sql STABLE PARALLEL SAFE SET search_path = '' AS $$
    SELECT CASE WHEN handdraw.valid_resource_id(current_setting('handdraw.user_id', true), 'usr')
        THEN current_setting('handdraw.user_id', true) END
$$;
REVOKE ALL ON FUNCTION handdraw.current_actor() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION handdraw.current_actor() TO handdraw_request;

CREATE TABLE handdraw.profiles (
    id text COLLATE "C" PRIMARY KEY CHECK (handdraw.valid_resource_id(id, 'usr')),
    auth_user_id uuid UNIQUE REFERENCES auth.users(id) ON DELETE SET NULL
        CHECK (auth_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 200 AND btrim(display_name) = display_name),
    avatar_url text,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    deleted_at timestamptz,
    CHECK (updated_at >= created_at),
    CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);
ALTER TABLE handdraw.profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE handdraw.profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY migration_profiles ON handdraw.profiles FOR ALL TO CURRENT_USER USING (true) WITH CHECK (true);
CREATE POLICY identity_lookup ON handdraw.profiles FOR SELECT TO handdraw_identity_owner USING (true);
CREATE POLICY identity_insert ON handdraw.profiles FOR INSERT TO handdraw_identity_owner WITH CHECK (true);
CREATE POLICY profile_self ON handdraw.profiles FOR SELECT TO handdraw_request
    USING (id = handdraw.current_actor() AND auth_user_id IS NOT NULL AND deleted_at IS NULL);
GRANT SELECT, INSERT ON handdraw.profiles TO handdraw_identity_owner;
GRANT SELECT (id, display_name, avatar_url, created_at, updated_at) ON handdraw.profiles TO handdraw_request;

CREATE FUNCTION handdraw.guard_profile_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (NEW.auth_user_id IS DISTINCT FROM OLD.auth_user_id AND NEW.auth_user_id IS NOT NULL) THEN
        RAISE EXCEPTION 'profile identity is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION handdraw.guard_profile_identity() FROM PUBLIC;
CREATE TRIGGER profile_identity_immutable BEFORE UPDATE ON handdraw.profiles
    FOR EACH ROW EXECUTE FUNCTION handdraw.guard_profile_identity();

-- The narrow definer needs UPDATE(id) only for the external identity's key-share lock.
GRANT USAGE ON SCHEMA auth TO handdraw_identity_owner;
GRANT SELECT (id), UPDATE (id) ON auth.users TO handdraw_identity_owner;
CREATE FUNCTION handdraw.resolve_profile(candidate_id text, subject uuid, initial_name text)
RETURNS TABLE(id text, auth_user_id uuid, display_name text, created_at timestamptz, updated_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
BEGIN
    PERFORM 1 FROM auth.users u WHERE u.id = subject FOR KEY SHARE;
    IF NOT FOUND THEN RETURN; END IF;
    INSERT INTO handdraw.profiles(id, auth_user_id, display_name)
    VALUES (candidate_id, subject, initial_name)
    ON CONFLICT ON CONSTRAINT profiles_auth_user_id_key DO NOTHING;
    RETURN QUERY SELECT p.id, p.auth_user_id, p.display_name, p.created_at, p.updated_at
        FROM handdraw.profiles p WHERE p.auth_user_id = subject AND p.deleted_at IS NULL;
END;
$$;
REVOKE ALL ON FUNCTION handdraw.resolve_profile(text,uuid,text) FROM PUBLIC;
GRANT CREATE ON SCHEMA handdraw TO handdraw_identity_owner;
ALTER FUNCTION handdraw.resolve_profile(text,uuid,text) OWNER TO handdraw_identity_owner;
REVOKE CREATE ON SCHEMA handdraw FROM handdraw_identity_owner;
GRANT EXECUTE ON FUNCTION handdraw.resolve_profile(text,uuid,text) TO handdraw_identity_resolver;

COMMIT;
