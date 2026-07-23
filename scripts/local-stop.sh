#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PG_BIN=${PG_BIN:-/usr/lib/postgresql/14/bin}
SERVER_PID="$ROOT/.local/server.pid"
PG_DATA="$ROOT/.local/postgres"

if [ -f "$SERVER_PID" ]; then
  pid=$(tr -d '\n' < "$SERVER_PID")
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid"
    for _ in $(seq 1 50); do
      if ! kill -0 "$pid" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "$pid" 2>/dev/null; then
      printf 'backend did not stop in time: pid=%s\n' "$pid" >&2
      exit 1
    fi
    printf 'backend stopped: pid=%s\n' "$pid"
  fi
  rm -f "$SERVER_PID"
fi

if [ -d "$PG_DATA" ] && "$PG_BIN/pg_ctl" -D "$PG_DATA" status >/dev/null 2>&1; then
  "$PG_BIN/pg_ctl" -D "$PG_DATA" stop -m fast
fi
