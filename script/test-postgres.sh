#!/bin/sh
# Testcontainers owns isolated PostgreSQL setup and cleanup inside the testify suite.
set -eu
server_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$server_dir"
exec go test -tags=integration -race -count=1 -timeout=5m ./internal/board/infra/db ./internal/identity/infra/db ./app/access/infra/db "$@"
