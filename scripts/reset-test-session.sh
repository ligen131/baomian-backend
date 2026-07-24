#!/usr/bin/env bash
set -Eeuo pipefail

root=${RESET_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
psql_cmd=${RESET_PSQL_CMD:-psql}
user_id=''
device_id=''
apply=0
confirmation=''

usage() {
  printf 'usage: %s --user <userId> --device <deviceId> [--apply --confirm RESET-TONIGHT]\n' "$0" >&2
}

while (( $# > 0 )); do
  case "$1" in
    --user)
      (( $# >= 2 )) || { usage; exit 2; }
      user_id=$2
      shift 2
      ;;
    --device)
      (( $# >= 2 )) || { usage; exit 2; }
      device_id=$2
      shift 2
      ;;
    --apply)
      apply=1
      shift
      ;;
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

if [[ -z "$user_id" || -z "$device_id" || "$user_id" == '*' || "$device_id" == '*' ]]; then
  printf '%s\n' 'explicit non-empty --user and --device values are required' >&2
  exit 2
fi
if (( apply == 1 )) && [[ "$confirmation" != 'RESET-TONIGHT' ]]; then
  printf '%s\n' 'apply requires --confirm RESET-TONIGHT' >&2
  exit 2
fi
if (( apply == 0 )) && [[ -n "$confirmation" ]]; then
  printf '%s\n' '--confirm is only valid together with --apply' >&2
  exit 2
fi

if [[ -z "${DATABASE_URL:-}" ]]; then
  env_file="$root/.env"
  if [[ ! -r "$env_file" ]]; then
    printf '%s\n' 'DATABASE_URL is not set and .env is not readable' >&2
    exit 2
  fi
  DATABASE_URL=$(grep -m 1 '^DATABASE_URL=' "$env_file" | cut -d= -f2- || true)
fi
if [[ -z "${DATABASE_URL:-}" ]]; then
  printf '%s\n' 'DATABASE_URL is required' >&2
  exit 2
fi

mapfile -d '' connection_values < <(DATABASE_URL="$DATABASE_URL" python3 - <<'PY'
import os
import sys
from urllib.parse import parse_qs, unquote, urlsplit

parsed = urlsplit(os.environ["DATABASE_URL"])
if parsed.scheme not in {"postgres", "postgresql"} or not parsed.hostname:
    raise SystemExit("DATABASE_URL must be a PostgreSQL URI")
values = (
    parsed.hostname,
    str(parsed.port or 5432),
    unquote(parsed.username or ""),
    unquote(parsed.password or ""),
    unquote(parsed.path.lstrip("/")),
    parse_qs(parsed.query).get("sslmode", [""])[0],
)
for value in values:
    sys.stdout.buffer.write(value.encode() + b"\0")
PY
)
if (( ${#connection_values[@]} != 6 )) || [[ -z "${connection_values[4]}" ]]; then
  printf '%s\n' 'DATABASE_URL must include a database name' >&2
  exit 2
fi

common_args=(-X -v ON_ERROR_STOP=1 -P pager=off -v "user_id=$user_id" -v "device_id=$device_id")
connection_env=(
  "PGHOST=${connection_values[0]}"
  "PGPORT=${connection_values[1]}"
  "PGUSER=${connection_values[2]}"
  "PGPASSWORD=${connection_values[3]}"
  "PGDATABASE=${connection_values[4]}"
  "PGSSLMODE=${connection_values[5]}"
)

if (( apply == 0 )); then
  printf 'DRY RUN: previewing tonight reset for user=%s device=%s\n' "$user_id" "$device_id"
  env "${connection_env[@]}" "$psql_cmd" "${common_args[@]}" <<'SQL'
WITH target AS (
    SELECT ns.id, ns.date, ns.phase, ns.conversation_turns
    FROM night_sessions ns
    WHERE ns.user_id = :'user_id'
      AND ns.date = (
          CURRENT_TIMESTAMP AT TIME ZONE COALESCE(
              (SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'),
              'Asia/Shanghai'
          )
      )::date
)
SELECT
    t.id AS session_id,
    t.date,
    t.phase,
    t.conversation_turns,
    (SELECT count(*) FROM conversation_turns ct WHERE ct.session_id = t.id) AS conversation_turn_count,
    (SELECT count(*) FROM memory_cards mc WHERE mc.session_id = t.id) AS memory_card_count,
    (
        SELECT count(*)
        FROM device_commands dc
        WHERE dc.user_id = :'user_id'
          AND dc.device_id = :'device_id'
          AND dc.status IN ('pending', 'dispatched')
          AND (dc.created_at AT TIME ZONE COALESCE(
              (SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'),
              'Asia/Shanghai'
          ))::date = t.date
    ) AS pending_command_count
FROM target t;
SQL
  printf '%s\n' 'No data was changed. Re-run with --apply --confirm RESET-TONIGHT to execute.'
  exit 0
fi

printf 'Applying tonight reset for user=%s device=%s\n' "$user_id" "$device_id"
env "${connection_env[@]}" "$psql_cmd" "${common_args[@]}" <<'SQL'
BEGIN;
WITH target AS MATERIALIZED (
    SELECT ns.id, ns.date
    FROM night_sessions ns
    WHERE ns.user_id = :'user_id'
      AND ns.date = (
          CURRENT_TIMESTAMP AT TIME ZONE COALESCE(
              (SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'),
              'Asia/Shanghai'
          )
      )::date
),
deleted_commands AS (
    DELETE FROM device_commands dc
    USING target t
    WHERE dc.user_id = :'user_id'
      AND dc.device_id = :'device_id'
      AND dc.status IN ('pending', 'dispatched')
      AND (dc.created_at AT TIME ZONE COALESCE(
          (SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'),
          'Asia/Shanghai'
      ))::date = t.date
    RETURNING dc.id
),
deleted_sessions AS (
    DELETE FROM night_sessions ns
    USING target t
    WHERE ns.id = t.id
    RETURNING ns.id
)
SELECT
    (SELECT count(*) FROM deleted_sessions) AS deleted_session_count,
    (SELECT count(*) FROM deleted_commands) AS deleted_command_count;
COMMIT;
SQL
printf '%s\n' 'reset completed; profile, device, historical dates, acknowledged commands, and device event audit were preserved'
