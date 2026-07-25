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

CREATE TEMP TABLE reset_seed_sessions (
    seed_key TEXT PRIMARY KEY,
    session_id UUID NOT NULL,
    run_id UUID NOT NULL,
    seed_date DATE NOT NULL,
    emotion TEXT NOT NULL,
    worry TEXT NOT NULL,
    tomorrow_task TEXT NOT NULL,
    comfort TEXT NOT NULL,
    guidance TEXT NOT NULL
) ON COMMIT DROP;

INSERT INTO reset_seed_sessions VALUES
(
    'D-3',
    md5(:'user_id' || ':reset-seed:D-3:session')::uuid,
    md5(:'user_id' || ':reset-seed:D-3:run')::uuid,
    (CURRENT_TIMESTAMP AT TIME ZONE COALESCE((SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'), 'Asia/Shanghai'))::date - INTERVAL '3 days',
    '轻松',
    '今天完成了几件一直惦记的小事，心里松了一些。',
    '明早列出最重要的一件事',
    '你已经做得很好，今晚可以放心休息。',
    'rain'
),
(
    'D-2',
    md5(:'user_id' || ':reset-seed:D-2:session')::uuid,
    md5(:'user_id' || ':reset-seed:D-2:run')::uuid,
    (CURRENT_TIMESTAMP AT TIME ZONE COALESCE((SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'), 'Asia/Shanghai'))::date - INTERVAL '2 days',
    '疲惫',
    '工作还有一些没有收尾，担心明天会来不及。',
    '明早先处理最紧急的十分钟',
    '剩下的事情交给明天，现在先把自己照顾好。',
    'breathing_46'
),
(
    'D-1',
    md5(:'user_id' || ':reset-seed:D-1:session')::uuid,
    md5(:'user_id' || ':reset-seed:D-1:run')::uuid,
    (CURRENT_TIMESTAMP AT TIME ZONE COALESCE((SELECT p.time_zone FROM profiles p WHERE p.user_id = :'user_id'), 'Asia/Shanghai'))::date - INTERVAL '1 day',
    '平静',
    '明天有新的安排，期待里也带着一点紧张。',
    '起床后确认今天的第一个安排',
    '不需要一次准备好所有答案，慢慢来就可以。',
    'rain'
);

INSERT INTO night_sessions (
    id, user_id, date, phase, box_closed, conversation_turns, selected_guidance,
    latest_ai_draft, created_at, updated_at
)
SELECT
    session_id, :'user_id', seed_date, 'SLEEPING', TRUE, 1, guidance,
    '{}'::jsonb, now(), now()
FROM reset_seed_sessions
ON CONFLICT (user_id, date) DO UPDATE SET
    phase = EXCLUDED.phase,
    box_closed = EXCLUDED.box_closed,
    conversation_turns = EXCLUDED.conversation_turns,
    selected_guidance = EXCLUDED.selected_guidance,
    updated_at = now();

INSERT INTO conversation_runs (
    id, user_id, device_id, night_session_id, date, status, completed_turns,
    guidance, guidance_status, started_at, finished_at, created_at, updated_at
)
SELECT
    seed.run_id, :'user_id', '', session.id, seed.seed_date,
    'completed', 1, seed.guidance, 'completed', now(), now(), now(), now()
FROM reset_seed_sessions seed
JOIN night_sessions session ON session.user_id = :'user_id' AND session.date = seed.seed_date
ON CONFLICT (id) DO UPDATE SET
    night_session_id = EXCLUDED.night_session_id,
    status = EXCLUDED.status,
    guidance = EXCLUDED.guidance,
    guidance_status = EXCLUDED.guidance_status,
    updated_at = now();

INSERT INTO memory_cards (
    id, session_id, run_id, user_id, date, emotion, worry, tomorrow_task,
    comfort, suggested_guidance, fallback, created_at, updated_at
)
SELECT
    md5(:'user_id' || ':reset-seed:' || seed.seed_key || ':card')::uuid,
    run.night_session_id, seed.run_id, :'user_id', seed.seed_date, seed.emotion,
    seed.worry, seed.tomorrow_task, seed.comfort, seed.guidance, FALSE, now(), now()
FROM reset_seed_sessions seed
JOIN conversation_runs run ON run.id = seed.run_id
ON CONFLICT (run_id) DO UPDATE SET
    emotion = EXCLUDED.emotion,
    worry = EXCLUDED.worry,
    tomorrow_task = EXCLUDED.tomorrow_task,
    comfort = EXCLUDED.comfort,
    suggested_guidance = EXCLUDED.suggested_guidance,
    updated_at = now();
COMMIT;
SQL
printf '%s\n' 'reset completed; profile, device, historical dates, acknowledged commands, and device event audit were preserved'
