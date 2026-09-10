-- External Auth stand-in only; application DDL is applied through golang-migrate.
CREATE SCHEMA auth;
CREATE TABLE auth.users(id uuid PRIMARY KEY, email text, email_confirmed_at timestamptz);
