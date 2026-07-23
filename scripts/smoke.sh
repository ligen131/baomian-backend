#!/usr/bin/env sh
set -eu

BASE_URL=${BASE_URL:-http://localhost:8325}
RUN_ID="$(date +%s)-$$"
USER_ID=${DEMO_USER_ID:-smoke-user-$RUN_ID}
DEVICE_ID=${DEFAULT_DEVICE_ID:-smoke-device-$RUN_ID}
EVENT_ID="smoke-event-$RUN_ID"

request() {
  method=$1
  path=$2
  body=${3-}
  if [ -n "$body" ]; then
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Content-Type: application/json" \
      -H "X-Demo-User-Id: $USER_ID" \
      --data "$body"
  else
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "X-Demo-User-Id: $USER_ID"
  fi
}

printf '%s\n' '[1/10] health'
request GET /api/v1/health >/dev/null

printf '%s\n' '[2/10] profile and tonight'
request GET /api/v1/profile >/dev/null
request GET /api/v1/tonight >/dev/null

printf '%s\n' '[3/10] close box'
request POST /api/v1/tonight/actions '{"action":"simulate_box_closed"}' >/dev/null

printf '%s\n' '[4/10] conversation turn (Claude or local fallback)'
request POST /api/v1/conversations/turn '{"text":"明天要做工作汇报，我有点紧张。","inputMode":"text"}' >/dev/null

printf '%s\n' '[5/10] finalize and select guidance'
request POST /api/v1/conversations/finalize '{}' >/dev/null
request POST /api/v1/tonight/actions '{"action":"select_guidance","guidance":"breathing_46"}' >/dev/null

printf '%s\n' '[6/10] journal'
request GET '/api/v1/journals?limit=7' >/dev/null

printf '%s\n' '[7/10] idempotent device event'
DEVICE_BODY=$(printf '{"eventId":"%s","deviceId":"%s","userId":"%s","type":"box_opened","payload":{}}' "$EVENT_ID" "$DEVICE_ID" "$USER_ID")
first=$(request POST /api/v1/device/events "$DEVICE_BODY")
second=$(request POST /api/v1/device/events "$DEVICE_BODY")
printf '%s' "$first" | grep -q '"duplicate":false'
printf '%s' "$second" | grep -q '"duplicate":true'

printf '%s\n' '[8/10] restore box and simulate alarm'
request POST /api/v1/tonight/actions '{"action":"simulate_box_closed"}' >/dev/null
request POST /api/v1/tonight/actions '{"action":"simulate_alarm"}' >/dev/null

printf '%s\n' '[9/10] snooze and wake'
request POST /api/v1/tonight/actions '{"action":"snooze"}' >/dev/null
request POST /api/v1/tonight/actions '{"action":"mark_awake"}' >/dev/null

printf '%s\n' '[10/10] take and ack one device command when available'
command=$(curl -sS -w '\n%{http_code}' "$BASE_URL/api/v1/device/commands/next?deviceId=$DEVICE_ID&timeoutSec=1")
status=$(printf '%s\n' "$command" | sed -n '$p')
body=$(printf '%s\n' "$command" | sed '$d')
if [ "$status" = "200" ]; then
  command_id=$(printf '%s' "$body" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  [ -n "$command_id" ]
  request POST /api/v1/device/commands/ack "{\"deviceId\":\"$DEVICE_ID\",\"commandId\":\"$command_id\",\"success\":true,\"payload\":{}}" >/dev/null
elif [ "$status" != "204" ]; then
  printf 'unexpected command status: %s\n%s\n' "$status" "$body" >&2
  exit 1
fi

printf '%s\n' 'smoke test passed'
