#!/usr/bin/env bash
set -Eeuo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
root="$tmp/app"
bin="$tmp/bin"
mkdir -p "$root/.local" "$root/cmd/server" "$bin"
printf '#!/usr/bin/env bash\nexit 0\n' > "$root/.local/baomian-server"
chmod +x "$root/.local/baomian-server"
printf '4242\n' > "$root/.local/server.pid"
printf 'old log\n' > "$root/.local/server.log"

cat > "$bin/go" <<'EOF'
#!/usr/bin/env bash
set -eu
printf 'go %s\n' "$*" >> "$RESTART_TEST_EVENTS"
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then output=$2; shift 2; continue; fi
  shift
done
printf '#!/usr/bin/env bash\nwhile true; do sleep 1; done\n' > "$output"
chmod +x "$output"
EOF
cat > "$bin/ss" <<'EOF'
#!/usr/bin/env bash
printf 'LISTEN 0 4096 0.0.0.0:8325 0.0.0.0:* users:(("baomian-server",pid=4242,fd=3))\n'
EOF
cat > "$bin/readlink" <<EOF
#!/usr/bin/env bash
case "\$1" in
  /proc/4242/exe) printf '%s\n' '$root/.local/baomian-server' ;;
  /proc/4242/cwd) printf '%s\n' '$root' ;;
  *) /usr/bin/readlink "\$@" ;;
esac
EOF
cat > "$bin/fake-kill" <<'EOF'
#!/usr/bin/env bash
printf 'kill %s\n' "$*" >> "$RESTART_TEST_EVENTS"
touch "$RESTART_TEST_STOPPED"
EOF
cat > "$bin/fake-alive" <<'EOF'
#!/usr/bin/env bash
if [ "$1" = '4242' ] && [ -f "$RESTART_TEST_STOPPED" ]; then exit 1; fi
exit 0
EOF
cat > "$bin/fake-start" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' 5252
EOF
cat > "$bin/curl" <<'EOF'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >> "$RESTART_TEST_EVENTS"
exit 0
EOF
chmod +x "$bin"/*

events="$tmp/events"
: > "$events"
PATH="$bin:/usr/bin:/bin" RESTART_ROOT="$root" RESTART_TEST_EVENTS="$events" \
  RESTART_TEST_STOPPED="$tmp/stopped" RESTART_KILL_CMD="$bin/fake-kill" \
  RESTART_IS_ALIVE_CMD="$bin/fake-alive" RESTART_START_CMD="$bin/fake-start" \
  "$repo/scripts/restart-from-source.sh"

grep -q '^go build -o .*/\.local/baomian-server.next ./cmd/server$' "$events"
grep -q '^kill -TERM 4242$' "$events"
grep -q '^curl .*http://127.0.0.1:8325/api/v1/health$' "$events"
test -x "$root/.local/baomian-server"
test -f "$root/.local/baomian-server.previous"
test "$(cat "$root/.local/server.pid")" = "5252"
printf '%s\n' 'restart-from-source test passed'
