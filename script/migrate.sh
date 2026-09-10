#!/bin/sh
set -eu

server_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
migrations_dir="$server_dir/migrations"

usage() {
  cat <<'USAGE'
Usage: sh script/migrate.sh COMMAND

  create NAME  Create sequential .up.sql/.down.sql files; edit and review both.
  up [N]       Apply all pending migrations or N migrations.
  down N       Roll back exactly N migrations.
  version      Read the target database's migration version.

up, down and version require exported MIGRATION_DATABASE_URL.
Use search_path=public and x-migrations-table=handdraw_schema_migrations in that URL.
This script never reads DATABASE_URL or sources an environment file.
Applied SQL files are immutable; golang-migrate does not check their checksums.
USAGE
}

fail() { printf '%s\n' "$1" >&2; exit 2; }

require_migrate() {
  command -v migrate >/dev/null 2>&1 || fail 'Required tool is not installed: migrate'
}

require_up_migrations() {
  for migration_file in "$migrations_dir"/*.up.sql; do
    if [ -f "$migration_file" ]; then return; fi
  done
  fail 'No up migrations exist. Run create NAME and write the migration SQL first.'
}

require_positive_count() {
  case "$1" in
    ''|0*|*[!0-9]*) fail 'Migration count must be a positive integer without leading zeros.' ;;
  esac
}

# Failed database commands can include credentials in their diagnostics.
run_tool() {
  if tool_output=$("$@" 2>&1); then
    if [ -n "$tool_output" ]; then printf '%s\n' "$tool_output"; fi
  else
    printf '%s\n' 'Migration command failed. Tool error output is hidden to avoid exposing connection details.' >&2
    exit 1
  fi
}

[ "$#" -gt 0 ] || { usage >&2; exit 2; }
action=$1
shift
case "$action" in
  create)
    [ "$#" -eq 1 ] || fail 'Usage: sh script/migrate.sh create NAME'
    case "$1" in
      ''|[!A-Za-z]*|*[!A-Za-z0-9_]*) fail 'Migration name must start with a letter and contain only letters, digits and underscores.' ;;
    esac
    require_migrate
    mkdir -p "$migrations_dir"
    run_tool migrate create -ext sql -dir "$migrations_dir" -seq -digits 6 "$1"
    ;;
  up|down|version)
    case "$action" in
      up)
        [ "$#" -le 1 ] || fail 'Usage: sh script/migrate.sh up [N]'
        if [ "$#" -eq 1 ]; then require_positive_count "$1"; fi
        ;;
      down)
        [ "$#" -eq 1 ] || fail 'Usage: sh script/migrate.sh down N'
        require_positive_count "$1"
        ;;
      version) [ "$#" -eq 0 ] || fail 'Usage: sh script/migrate.sh version' ;;
    esac
    [ -n "${MIGRATION_DATABASE_URL:-}" ] || fail 'Export MIGRATION_DATABASE_URL before running a target migration command.'
    case "$MIGRATION_DATABASE_URL" in
      postgres://*\?*|postgresql://*\?*) ;;
      *) fail 'Use a PostgreSQL URL with explicit migration history settings.' ;;
    esac
    migration_options="&${MIGRATION_DATABASE_URL#*\?}&"
    case "$migration_options" in
      *'&search_path='*'&search_path='*|*'&x-migrations-table='*'&x-migrations-table='*)
        fail 'Migration history settings must not be duplicated.' ;;
    esac
    case "$migration_options" in
      *'&search_path=public&'*) ;;
      *) fail 'Migration URL must set search_path=public.' ;;
    esac
    case "$migration_options" in
      *'&x-migrations-table=handdraw_schema_migrations&'*) ;;
      *) fail 'Migration URL must set x-migrations-table=handdraw_schema_migrations.' ;;
    esac
    require_up_migrations
    require_migrate
    run_tool migrate -lock-timeout 15 -path "$migrations_dir" -database "$MIGRATION_DATABASE_URL" "$action" "$@"
    ;;
  help|-h|--help)
    [ "$#" -eq 0 ] || fail 'Usage: sh script/migrate.sh help'
    usage
    ;;
  *) fail 'Unknown command. Use: sh script/migrate.sh help' ;;
esac
