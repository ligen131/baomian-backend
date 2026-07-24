#!/usr/bin/env sh
set -eu

BASE_URL=${BASE_URL:-http://localhost:8325}
RUN_ID="$(date +%s)-$$"
USER_ID=${DEMO_USER_ID:-smoke-user-$RUN_ID}
DEVICE_ID=${DEFAULT_DEVICE_ID:-smoke-device-$RUN_ID}
EVENT_ID="smoke-event-$RUN_ID"
REQUEST_ID="smoke-request-$RUN_ID"

request() {
  method=$1
  path=$2
  body=${3-}
  if [ -n "$body" ]; then
    curl -fsS -X "$method" "$BASE_URL$path" -H "Content-Type: application/json" -H "X-Demo-User-Id: $USER_ID" --data "$body"
  else
    curl -fsS -X "$method" "$BASE_URL$path" -H "X-Demo-User-Id: $USER_ID"
  fi
}

printf '%s\n' '[1/12] health and metrics'
request GET /api/v1/health >/dev/null
if [ "${CHECK_METRICS:-1}" = "1" ]; then
  curl -fsS "$BASE_URL/metrics" | grep -q baomian_http_requests_total
fi

printf '%s\n' '[2/12] profile timezone and reminders'
request PUT /api/v1/profile '{"timeZone":"Asia/Shanghai","bedtimeReminderEnabled":true,"wakeAlarmEnabled":true}' | grep -q '"timeZone":"Asia/Shanghai"'
request POST /api/v1/tonight/actions '{"action":"skip_tonight_reminders"}' | grep -q '"remindersSkipped":true'

printf '%s\n' '[3/12] close box and start conversation'
request POST /api/v1/tonight/actions '{"action":"simulate_box_closed"}' >/dev/null
request POST /api/v1/tonight/actions '{"action":"start_conversation"}' >/dev/null
request POST /api/v1/conversations/activity '{"activity":"typing"}' | grep -q 'conversationSilenceDeadlineAt'

printf '%s\n' '[4/12] idempotent voice turn'
TURN_BODY=$(printf '{"text":"明天要做工作汇报，我有点紧张。","inputMode":"voice","clientRequestId":"%s"}' "$REQUEST_ID")
first_turn=$(request POST /api/v1/conversations/turn "$TURN_BODY")
second_turn=$(request POST /api/v1/conversations/turn "$TURN_BODY")
printf '%s' "$first_turn" | grep -q '"fallback"'
printf '%s' "$second_turn" | grep -q '"reply"'
request GET /api/v1/conversations/tonight | grep -q '"inputMode":"voice"'

printf '%s\n' '[5/12] finalize and select timed guidance'
finalized=$(request POST /api/v1/conversations/finalize '{}')
journal_id=$(printf '%s' "$finalized" | sed -n 's/.*"journal":{"id":"\([^"]*\)".*/\1/p')
[ -n "$journal_id" ]
request POST /api/v1/tonight/actions '{"action":"select_guidance","guidance":"rain"}' | grep -q 'audioEndsAt'

printf '%s\n' '[6/12] journal detail and task completion'
request GET "/api/v1/journals/$journal_id" >/dev/null
request PATCH "/api/v1/journals/$journal_id" '{"tomorrowTaskCompleted":true}' | grep -q '"tomorrowTaskCompleted":true'

printf '%s\n' '[7/12] device heartbeat and status'
HEARTBEAT=$(printf '{"deviceId":"%s","userId":"%s","firmwareVersion":"smoke","capabilities":{"audio":true},"status":{"boxClosed":true}}' "$DEVICE_ID" "$USER_ID")
request POST /api/v1/device/heartbeat "$HEARTBEAT" | grep -q '"online":true'
request GET "/api/v1/devices/$DEVICE_ID/status" | grep -q '"online":true'

printf '%s\n' '[8/12] idempotent device event'
DEVICE_BODY=$(printf '{"eventId":"%s","deviceId":"%s","userId":"%s","type":"box_opened","payload":{}}' "$EVENT_ID" "$DEVICE_ID" "$USER_ID")
first=$(request POST /api/v1/device/events "$DEVICE_BODY")
second=$(request POST /api/v1/device/events "$DEVICE_BODY")
printf '%s' "$first" | grep -q '"duplicate":false'
printf '%s' "$second" | grep -q '"duplicate":true'

printf '%s\n' '[9/12] command lease response'
command=$(curl -sS -w '\n%{http_code}' "$BASE_URL/api/v1/device/commands/next?deviceId=$DEVICE_ID&timeoutSec=1")
status=$(printf '%s\n' "$command" | sed -n '$p')
body=$(printf '%s\n' "$command" | sed '$d')
if [ "$status" = "200" ]; then
  printf '%s' "$body" | grep -q '"attempt":1'
  printf '%s' "$body" | grep -q 'leaseExpiresAt'
  command_id=$(printf '%s' "$body" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  request POST /api/v1/device/commands/ack "{\"deviceId\":\"$DEVICE_ID\",\"commandId\":\"$command_id\",\"success\":true,\"payload\":{}}" >/dev/null
elif [ "$status" != "204" ]; then
  printf 'unexpected command status: %s\n%s\n' "$status" "$body" >&2
  exit 1
fi

printf '%s\n' '[10/12] restore box and simulate alarm'
request POST /api/v1/tonight/actions '{"action":"simulate_box_closed"}' >/dev/null
request POST /api/v1/tonight/actions '{"action":"simulate_alarm"}' >/dev/null

printf '%s\n' '[11/12] snooze and wake'
request POST /api/v1/tonight/actions '{"action":"snooze"}' >/dev/null
request POST /api/v1/tonight/actions '{"action":"mark_awake"}' >/dev/null

printf '%s\n' '[12/12] delete historical journal and conversation'
http_status=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE_URL/api/v1/journals/$journal_id" -H "X-Demo-User-Id: $USER_ID")
[ "$http_status" = "204" ]

printf '%s\n' 'P0+P1 smoke test passed'
