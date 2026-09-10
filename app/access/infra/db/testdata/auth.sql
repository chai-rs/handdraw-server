-- External Supabase Auth stand-in only. Application DDL comes from migrations/.
CREATE SCHEMA auth;
CREATE TABLE auth.users(id uuid PRIMARY KEY, email text, email_confirmed_at timestamptz);
