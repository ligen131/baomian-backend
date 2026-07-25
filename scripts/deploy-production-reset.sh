#!/usr/bin/env bash
set -Eeuo pipefail

root=${DEPLOY_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
env_file=${DEPLOY_ENV_FILE:-$root/.env}
confirmation=''
public_health_url=${DEPLOY_PUBLIC_HEALTH_URL:-https://bm.lg.gl/api/v1/health}
run_tests=${DEPLOY_RUN_TESTS:-1}
backup=''
rollback_binary="$root/.local/baomian-server.pre-deploy"
schema_changed=0
old_server_running=0
deploy_succeeded=0

usage() {
  printf 'usage: %s --confirm RESET-PRODUCTION\n' "$0" >&2
  printf '%s\n' 'Backs up the configured database, removes all data, runs migrations, seeds demo journals, restarts the server, and verifies health.' >&2
}

while (( $# > 0 )); do
  case "$1" in
    --confirm)
      (( $# >= 2 )) || { usage; exit 2; }
      confirmation=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

if [[ "$confirmation" != 'RESET-PRODUCTION' ]]; then
  printf '%s\n' 'exact confirmation RESET-PRODUCTION is required' >&2
  usage
  exit 2
fi

for command in go pg_dump pg_restore psql curl ss readlink python3; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'required command not found: %s\n' "$command" >&2
    exit 1
  }
done
if [[ ! -r "$env_file" ]]; then
  printf 'production env file is not readable: %s\n' "$env_file" >&2
  exit 1
fi
chmod 0600 "$env_file"

ensure_env_setting() {
  local key=$1 value=$2
  ENV_FILE="$env_file" ENV_KEY="$key" ENV_VALUE="$value" python3 - <<'PY'
import os
from pathlib import Path

path = Path(os.environ["ENV_FILE"])
key = os.environ["ENV_KEY"]
value = os.environ["ENV_VALUE"]
lines = path.read_text().splitlines()
updated = False
for index, line in enumerate(lines):
    stripped = line.strip()
    if not stripped or stripped.startswith("#") or "=" not in line:
        continue
    if line.split("=", 1)[0].strip() == key:
        lines[index] = f"{key}={value}"
        updated = True
if not updated:
    if lines and lines[-1] != "":
        lines.append("")
    lines.append(f"{key}={value}")
path.write_text("\n".join(lines).rstrip() + "\n")
path.chmod(0o600)
PY
}

ensure_env_setting VOICE_MAX_UTTERANCE_DURATION 60s
ensure_env_setting DEMO_RAIN_AUDIO_PATH /home/ligen/tmp/rainy.wav
ensure_env_setting DEMO_BREATHING_AUDIO_PATH /home/ligen/tmp/miao.mp3

set -a
# shellcheck disable=SC1090
. "$env_file"
set +a

: "${DATABASE_URL:?DATABASE_URL is required in the production env file}"
: "${DEMO_USER_ID:?DEMO_USER_ID is required in the production env file}"
: "${DEFAULT_DEVICE_ID:?DEFAULT_DEVICE_ID is required in the production env file}"

for audio_file in "$DEMO_RAIN_AUDIO_PATH" "$DEMO_BREATHING_AUDIO_PATH"; do
  if [[ ! -r "$audio_file" ]]; then
    printf 'sleep audio file is not readable: %s\n' "$audio_file" >&2
    exit 1
  fi
done

mapfile -d '' database_identity < <(DATABASE_URL="$DATABASE_URL" python3 - <<'PY'
import os
import sys
from urllib.parse import unquote, urlsplit

parsed = urlsplit(os.environ["DATABASE_URL"])
if parsed.scheme not in {"postgres", "postgresql"} or not parsed.hostname:
    raise SystemExit("DATABASE_URL must be a PostgreSQL URI")
database = unquote(parsed.path.lstrip("/"))
if not database or database in {"postgres", "template0", "template1"}:
    raise SystemExit("refusing to reset an empty or PostgreSQL system database")
for value in (parsed.hostname, database):
    sys.stdout.buffer.write(value.encode() + b"\0")
PY
)
if (( ${#database_identity[@]} != 2 )); then
  printf '%s\n' 'failed to identify the production database' >&2
  exit 1
fi
printf 'target database: host=%s database=%s\n' "${database_identity[0]}" "${database_identity[1]}"

cd "$root"
printf '%s\n' '[1/8] verifying source before interruption'
if [[ "$run_tests" == '1' ]]; then
  go test ./... -count=1
fi
go build -o .local/baomian-server.preflight ./cmd/server
rm -f .local/baomian-server.preflight

printf '%s\n' '[2/8] backing up current database'
backup=$(DATABASE_URL="$DATABASE_URL" BACKUP_DIR="$root/.local/backups" "$root/scripts/backup.sh")
printf 'backup created: %s\n' "$backup"

listener_output=$(ss -ltnp 'sport = :8325')
mapfile -t listener_pids < <(printf '%s\n' "$listener_output" | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u || true)
if (( ${#listener_pids[@]} > 1 )); then
  printf 'refusing deployment: multiple listeners on port 8325: %s\n' "${listener_pids[*]}" >&2
  exit 1
fi
if (( ${#listener_pids[@]} == 1 )); then
  old_pid=${listener_pids[0]}
  old_exe=$(readlink "/proc/$old_pid/exe")
  old_cwd=$(readlink "/proc/$old_pid/cwd")
  if [[ "$old_exe" != "$root/.local/baomian-server" || "$old_cwd" != "$root" ]]; then
    printf 'refusing deployment: pid %s on port 8325 is not this repository server\n' "$old_pid" >&2
    exit 1
  fi
  old_server_running=1
  cp -p "$root/.local/baomian-server" "$rollback_binary"
fi

start_installed_server() {
  if ss -ltnp 'sport = :8325' | grep -q 'pid='; then
    return 0
  fi
  if [[ ! -x "$root/.local/baomian-server" ]]; then
    return 1
  fi
  (
    cd "$root"
    nohup "$root/.local/baomian-server" >> "$root/.local/server.log" 2>&1 </dev/null &
    printf '%s\n' "$!" > "$root/.local/server.pid"
  )
  for _ in {1..200}; do
    if curl -fsS --max-time 1 http://127.0.0.1:8325/api/v1/health >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

rollback() {
  local code=$?
  trap - EXIT
  if (( code == 0 || deploy_succeeded == 1 )); then
    exit "$code"
  fi
  set +e
  printf '%s\n' 'deployment failed; attempting rollback' >&2
  current_pids=$(ss -ltnp 'sport = :8325' | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u || true)
  for pid in $current_pids; do
    kill -TERM "$pid" 2>/dev/null || true
  done
  sleep 1
  if (( schema_changed == 1 )) && [[ -r "$backup" ]]; then
    psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 <<'SQL'
DROP SCHEMA IF EXISTS public CASCADE;
CREATE SCHEMA public;
GRANT ALL ON SCHEMA public TO CURRENT_USER;
GRANT ALL ON SCHEMA public TO public;
SQL
    TARGET_DATABASE_URL="$DATABASE_URL" "$root/scripts/restore.sh" "$backup"
  fi
  if (( old_server_running == 1 )); then
    if [[ -x "$rollback_binary" ]]; then
      cp -f "$rollback_binary" "$root/.local/baomian-server"
      chmod 0755 "$root/.local/baomian-server"
    fi
    start_installed_server || printf '%s\n' 'rollback could not restart the previous server' >&2
  fi
  exit "$code"
}
trap rollback EXIT

printf '%s\n' '[3/8] stopping the current server'
if (( old_server_running == 1 )); then
  kill -TERM "$old_pid"
  for _ in {1..100}; do
    if ! kill -0 "$old_pid" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  if kill -0 "$old_pid" 2>/dev/null; then
    printf 'server pid %s did not stop after 10 seconds\n' "$old_pid" >&2
    exit 1
  fi
fi

printf '%s\n' '[4/8] completely initializing the database'
schema_changed=1
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 <<'SQL'
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
GRANT ALL ON SCHEMA public TO CURRENT_USER;
GRANT ALL ON SCHEMA public TO public;
SQL
go run ./cmd/migrate up

printf '%s\n' '[5/8] seeding the three historical demo journals'
"$root/scripts/reset-test-session.sh" \
  --user "$DEMO_USER_ID" \
  --device "$DEFAULT_DEVICE_ID" \
  --apply \
  --confirm RESET-TONIGHT

printf '%s\n' '[6/8] restarting from the current source tree'
RESTART_PORT=8325 RESTART_RUN_TESTS=0 "$root/scripts/restart-from-source.sh"

printf '%s\n' '[7/8] verifying database and process identity'
migration_state=$(psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -Atc "SELECT version || ':' || dirty FROM schema_migrations;")
[[ "$migration_state" == '4:false' ]] || {
  printf 'unexpected migration state: %s\n' "$migration_state" >&2
  exit 1
}
seed_journals=$(psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -At \
  -v "user_id=$DEMO_USER_ID" <<'SQL'
SELECT count(*) FROM memory_cards WHERE user_id = :'user_id';
SQL
)
[[ "$seed_journals" == '3' ]] || {
  printf 'unexpected seed journal count: %s\n' "$seed_journals" >&2
  exit 1
}
printf 'migration=4 clean\nseed_journals=3\n'
new_pid=$(tr -d '\n' < "$root/.local/server.pid")
[[ "$(readlink "/proc/$new_pid/exe")" == "$root/.local/baomian-server" ]]
[[ "$(readlink "/proc/$new_pid/cwd")" == "$root" ]]
curl -fsS --max-time 5 http://127.0.0.1:8325/api/v1/health >/dev/null

printf '%s\n' '[8/8] verifying public health'
curl -fsS --max-time 15 "$public_health_url" >/dev/null

deploy_succeeded=1
trap - EXIT
rm -f "$rollback_binary"
printf 'production deployment completed: pid=%s backup=%s publicHealth=%s\n' "$new_pid" "$backup" "$public_health_url"
