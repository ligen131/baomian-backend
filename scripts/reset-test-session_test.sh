#!/usr/bin/env bash
set -Eeuo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
root="$tmp/app"
bin="$tmp/bin"
mkdir -p "$root" "$bin"
printf 'DATABASE_URL=%s\n' 'postgres://secret-user:secret-password@db.example/baomian' > "$root/.env"
chmod 600 "$root/.env"

cat > "$bin/psql" <<'EOF'
#!/usr/bin/env bash
set -eu
count_file="$RESET_TEST_TMP/count"
count=0
if [[ -f "$count_file" ]]; then count=$(<"$count_file"); fi
count=$((count + 1))
printf '%s\n' "$count" > "$count_file"
printf '%s\n' "$@" > "$RESET_TEST_TMP/args-$count"
tee "$RESET_TEST_TMP/sql-$count" >/dev/null
printf '%s\n' 'session_id | date | phase | conversation_turns | conversation_turn_count | memory_card_count | pending_command_count'
printf '%s\n' 'test-session | 2026-07-24 | CONVERSATION | 1 | 2 | 0 | 3'
EOF
chmod +x "$bin/psql"

run_reset() {
  RESET_ROOT="$root" RESET_PSQL_CMD="$bin/psql" RESET_TEST_TMP="$tmp" \
    "$repo/scripts/reset-test-session.sh" "$@"
}

user="test';DROP TABLE profiles;--"
device="device';DELETE FROM devices;--"
output=$(run_reset --user "$user" --device "$device")
grep -q 'DRY RUN' <<<"$output"
grep -q 'test-session' <<<"$output"
! grep -q 'secret-password' <<<"$output"
grep -q '^SELECT\|^WITH' "$tmp/sql-1"
! grep -qi 'delete\|begin\|commit' "$tmp/sql-1"
! grep -Fq "$user" "$tmp/sql-1"
! grep -Fq "$device" "$tmp/sql-1"
grep -Fxq "user_id=$user" "$tmp/args-1"
grep -Fxq "device_id=$device" "$tmp/args-1"
! grep -q 'secret-password' "$tmp/args-1"

if run_reset --user test-user >/dev/null 2>&1; then
  printf '%s\n' 'missing device must fail' >&2
  exit 1
fi
if run_reset --user test-user --device test-device --apply >/dev/null 2>&1; then
  printf '%s\n' 'apply without confirmation must fail' >&2
  exit 1
fi
if run_reset --user test-user --device test-device --apply --confirm WRONG >/dev/null 2>&1; then
  printf '%s\n' 'wrong confirmation must fail' >&2
  exit 1
fi

output=$(run_reset --user test-user --device test-device --apply --confirm RESET-TONIGHT)
grep -q 'reset completed' <<<"$output"
! grep -q 'secret-password' <<<"$output"
grep -qi '^BEGIN;' "$tmp/sql-2"
grep -qi 'DELETE FROM device_commands' "$tmp/sql-2"
grep -qi 'DELETE FROM night_sessions' "$tmp/sql-2"
grep -qi '^COMMIT;' "$tmp/sql-2"
grep -Fxq 'user_id=test-user' "$tmp/args-2"
grep -Fxq 'device_id=test-device' "$tmp/args-2"
! grep -q 'secret-password' "$tmp/args-2"

printf '%s\n' 'reset-test-session test passed'
