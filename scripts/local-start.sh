#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PG_BIN=${PG_BIN:-/usr/lib/postgresql/14/bin}
PG_DATA="$ROOT/.local/postgres"
PG_LOG="$ROOT/.local/postgres.log"
SERVER="$ROOT/.local/baomian-server"
SERVER_LOG="$ROOT/.local/server.log"
SERVER_PID="$ROOT/.local/server.pid"

if [ ! -x "$PG_BIN/pg_ctl" ]; then
  printf 'pg_ctl not found: %s\n' "$PG_BIN/pg_ctl" >&2
  exit 1
fi
if [ ! -d "$PG_DATA" ]; then
  printf 'local PostgreSQL data directory not found: %s\n' "$PG_DATA" >&2
  exit 1
fi

mkdir -p "$ROOT/.local"
if ! "$PG_BIN/pg_ctl" -D "$PG_DATA" status >/dev/null 2>&1; then
  "$PG_BIN/pg_ctl" -D "$PG_DATA" -l "$PG_LOG" start
fi

GOMODCACHE=${GOMODCACHE:-/tmp/baomian-gomodcache} go -C "$ROOT" build -o "$SERVER" ./cmd/server

if [ -f "$SERVER_PID" ]; then
  old_pid=$(tr -d '\n' < "$SERVER_PID")
  if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
    printf 'backend already running: pid=%s\n' "$old_pid"
    exit 0
  fi
fi

cd "$ROOT"
nohup "$SERVER" > "$SERVER_LOG" 2>&1 &
printf '%s\n' "$!" > "$SERVER_PID"

for _ in $(seq 1 15); do
  if curl -fsS http://127.0.0.1:8325/api/v1/health >/dev/null 2>&1; then
    printf 'backend started: http://127.0.0.1:8325/api/v1 (pid=%s)\n' "$(tr -d '\n' < "$SERVER_PID")"
    exit 0
  fi
  sleep 1
done

printf 'backend failed to become healthy; see %s\n' "$SERVER_LOG" >&2
exit 1
