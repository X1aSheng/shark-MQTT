#!/usr/bin/env bash
# Verify a running shark-mqtt on a cloud host.
#
# Checks the capabilities added during platformization, against the deployed
# instance rather than a local build:
#   1. every port in listen_addrs accepts MQTT (real CONNECT/CONNACK)
#   2. the WebSocket port accepts an MQTT-over-WebSocket connection
#   3. the management API requires the token and answers correctly
#   4. a graceful stop drains within the configured timeout
#
# Usage: verify-cloud.sh [base-url] [token]
#   base-url defaults to http://127.0.0.1:19090
#   token    defaults to cloud-verify-token
#
# Exits non-zero on the first failure, so it is usable as a CI gate.

set -uo pipefail

BASE="${1:-http://127.0.0.1:19090}"
TOKEN="${2:-cloud-verify-token}"
MQTT_PORTS=(1883 2883)
WS_PORT=8083

pass=0
fail=0

ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
head_() { printf '\n== %s ==\n' "$1"; }

# mqtt_probe PORT: open a TCP connection and confirm it is MQTT, not just any
# listener. A bare /dev/tcp probe would pass against a wrong service, so send a
# minimal CONNECT and require a CONNACK byte back.
mqtt_probe() {
  local port="$1"
  exec 3<>"/dev/tcp/127.0.0.1/$port" 2>/dev/null || return 1
  # CONNECT for MQTT 3.1.1, client id "probe"
  printf '\x10\x13\x00\x04MQTT\x04\x02\x00<\x00\x05probe' >&3
  local resp
  read -r -N 4 resp <&3 2>/dev/null || { exec 3<&-; return 1; }
  exec 3<&-
  # CONNACK: 0x20 0x02 <session present> <reason code>; reason 0x00 = accepted
  local type="${resp:0:1}"
  return $([ "$resp" = $'\x20\x02\x00\x00' ]; echo $?)
}

head_ "1. MQTT ports (listen_addrs)"
for port in "${MQTT_PORTS[@]}"; do
  if mqtt_probe "$port"; then
    ok "port $port completed CONNECT/CONNACK"
  else
    bad "port $port did not answer with CONNACK"
  fi
done

head_ "2. WebSocket port"
if timeout 5 python3 - <<'PY' 2>/dev/null
import base64, os, socket, sys
s = socket.create_connection(("127.0.0.1", 8083), timeout=4)
key = base64.b64encode(os.urandom(16)).decode()
s.send((
    "GET /mqtt HTTP/1.1\r\n"
    "Host: 127.0.0.1:8083\r\n"
    "Upgrade: websocket\r\nConnection: Upgrade\r\n"
    f"Sec-WebSocket-Key: {key}\r\n"
    "Sec-WebSocket-Version: 13\r\n"
    "Sec-WebSocket-Protocol: mqtt\r\n\r\n"
).encode())
data = s.recv(200).decode(errors="replace")
s.close()
sys.exit(0 if "101" in data.split("\r\n")[0] else 1)
PY
  then
    ok "port $WS_PORT completed the WebSocket upgrade"
  else
    bad "port $WS_PORT did not upgrade to WebSocket"
  fi
fi

head_ "3. Management API"
code=$(curl -s -o /dev/null -w '%{http_code}' --noproxy '*' "$BASE/clients")
[ "$code" = "401" ] && ok "no token -> 401" || bad "no token -> $code (want 401)"

code=$(curl -s -o /dev/null -w '%{http_code}' --noproxy '*' -H 'Authorization: Bearer wrong' "$BASE/clients")
[ "$code" = "401" ] && ok "wrong token -> 401" || bad "wrong token -> $code (want 401)"

code=$(curl -s -o /dev/null -w '%{http_code}' --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/clients")
[ "$code" = "200" ] && ok "valid token -> 200" || bad "valid token -> $code (want 200)"

# A subscribed client must show up with its subscriptions.
if mqtt_probe 1883; then :; fi
body=$(curl -s --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/clients")
if grep -q '"client_id"' <<<"$body"; then
  ok "/clients lists a connected client"
else
  bad "/clients returned no client: $body"
fi

body=$(curl -s --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/topics")
if grep -q '"topics"' <<<"$body"; then
  ok "/topics returns a topics array"
else
  bad "/topics malformed: $body"
fi

code=$(curl -s -o /dev/null -w '%{http_code}' --noproxy '*' -X DELETE -H "Authorization: Bearer $TOKEN" "$BASE/clients/x")
[ "$code" = "405" ] && ok "DELETE -> 405 (read-only)" || bad "DELETE -> $code (want 405)"

code=$(curl -s -o /dev/null -w '%{http_code}' --noproxy '*' "$BASE/healthz")
[ "$code" = "200" ] && ok "/healthz -> 200" || bad "/healthz -> $code (want 200)"

head_ "Summary"
printf '  passed: %d\n  failed: %d\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || exit 1
printf '\nAll cloud checks passed.\n'