#!/usr/bin/env bash
set -Eeuo pipefail

root=${RESTART_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
local_dir="$root/.local"
binary="$local_dir/baomian-server"
next_binary="$local_dir/baomian-server.next"
previous_binary="$local_dir/baomian-server.previous"
failed_binary="$local_dir/baomian-server.failed"
pid_file="$local_dir/server.pid"
log_file="$local_dir/server.log"
previous_log="$local_dir/server.log.previous"
port=${RESTART_PORT:-8325}
health_url=${RESTART_HEALTH_URL:-http://127.0.0.1:$port/api/v1/health}
run_tests=${RESTART_RUN_TESTS:-0}

GO_CMD=${RESTART_GO_CMD:-go}
SS_CMD=${RESTART_SS_CMD:-ss}
READLINK_CMD=${RESTART_READLINK_CMD:-readlink}
CURL_CMD=${RESTART_CURL_CMD:-curl}
KILL_CMD=${RESTART_KILL_CMD:-}
IS_ALIVE_CMD=${RESTART_IS_ALIVE_CMD:-}
START_CMD=${RESTART_START_CMD:-}

mkdir -p "$local_dir"

process_alive() {
  local pid=$1
  if [[ -n "$IS_ALIVE_CMD" ]]; then
    "$IS_ALIVE_CMD" "$pid"
  else
    kill -0 "$pid" 2>/dev/null
  fi
}

send_signal() {
  local signal=$1 pid=$2
  if [[ -n "$KILL_CMD" ]]; then
    "$KILL_CMD" "-$signal" "$pid"
  else
    kill "-$signal" "$pid"
  fi
}

start_server() {
  if [[ -n "$START_CMD" ]]; then
    "$START_CMD" "$root" "$binary" "$log_file"
    return
  fi
  (
    cd "$root"
    nohup "$binary" >> "$log_file" 2>&1 </dev/null &
    printf '%s\n' "$!"
  )
}

wait_stopped() {
  local pid=$1
  for _ in {1..100}; do
    if ! process_alive "$pid"; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

wait_healthy() {
  local pid=$1
  for _ in {1..200}; do
    if ! process_alive "$pid"; then
      return 1
    fi
    if "$CURL_CMD" -fsS --max-time 1 "$health_url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

cd "$root"
if [[ "$run_tests" == "1" ]]; then
  "$GO_CMD" test ./... -count=1
fi
"$GO_CMD" build -o "$next_binary" ./cmd/server
chmod 0755 "$next_binary"

listener_output=$("$SS_CMD" -ltnp "sport = :$port")
mapfile -t listener_pids < <(printf '%s\n' "$listener_output" | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u || true)
if (( ${#listener_pids[@]} > 1 )); then
  printf 'refusing restart: multiple listeners on port %s: %s\n' "$port" "${listener_pids[*]}" >&2
  exit 1
fi

old_pid=''
if (( ${#listener_pids[@]} == 1 )); then
  old_pid=${listener_pids[0]}
  old_exe=$("$READLINK_CMD" "/proc/$old_pid/exe")
  old_cwd=$("$READLINK_CMD" "/proc/$old_pid/cwd")
  if [[ "$old_exe" != "$binary" || "$old_cwd" != "$root" ]]; then
    printf 'refusing restart: pid %s is not this repository server\n' "$old_pid" >&2
    exit 1
  fi
  send_signal TERM "$old_pid"
  if ! wait_stopped "$old_pid"; then
    printf 'server pid %s did not stop after 10 seconds\n' "$old_pid" >&2
    exit 1
  fi
fi

installed=0
new_pid=''
rollback() {
  local code=$?
  trap - EXIT
  if (( code == 0 || installed == 0 )); then
    exit "$code"
  fi
  set +e
  if [[ -n "$new_pid" ]] && process_alive "$new_pid"; then
    send_signal TERM "$new_pid"
    wait_stopped "$new_pid"
  fi
  [[ -f "$binary" ]] && mv -f "$binary" "$failed_binary"
  if [[ -f "$previous_binary" ]]; then
    mv -f "$previous_binary" "$binary"
    restored_pid=$(start_server)
    printf '%s\n' "$restored_pid" > "$pid_file.tmp"
    mv -f "$pid_file.tmp" "$pid_file"
    printf 'restart failed; restored previous server as pid %s\n' "$restored_pid" >&2
  fi
  exit "$code"
}
trap rollback EXIT

[[ -f "$binary" ]] && mv -f "$binary" "$previous_binary"
mv -f "$next_binary" "$binary"
installed=1
[[ -f "$log_file" ]] && mv -f "$log_file" "$previous_log"
new_pid=$(start_server)
printf '%s\n' "$new_pid" > "$pid_file.tmp"
mv -f "$pid_file.tmp" "$pid_file"

if ! wait_healthy "$new_pid"; then
  printf 'new server pid %s failed health check: %s\n' "$new_pid" "$health_url" >&2
  false
fi

trap - EXIT
printf 'server restarted from source: pid=%s health=%s\n' "$new_pid" "$health_url"
