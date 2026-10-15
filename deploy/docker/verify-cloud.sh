#!/usr/bin/env bash
# Verify a running shark-mqtt on a cloud host.
#
# Checks the capabilities added during platformization, against the deployed
# instance rather than a local build:
#   1. an unauthenticated CONNECT is refused on every MQTT port
#   2. an authenticated CONNECT gets a CONNACK on every MQTT port
#   3. the WebSocket port accepts an MQTT-over-WebSocket upgrade
#   4. the management API requires the token, answers correctly, and is read-only
#   5. /healthz and /readyz behave
#
# Sending a bare CONNECT and expecting success would be wrong here: the broker
# is configured with a credential file, so an anonymous client MUST be rejected.
# Both directions are checked, because "accepts everything" is the failure mode
# that actually matters.
#
# Usage: verify-cloud.sh [base-url] [token] [username] [password]
# Defaults: http://127.0.0.1:19090  cloud-verify-token  cloud-verify  cloud-verify-pass

set -uo pipefail

BASE="${1:-http://127.0.0.1:19090}"
TOKEN="${2:-cloud-verify-token}"
USER="${3:-cloud-verify}"
PASS="${4:-cloud-verify-pass}"
MQTT_PORTS=(1883 2883)
WS_PORT=8083

pass=0
fail=0
ok()    { printf '  \033[32mPASS\033[0m %s\n' "$1"; pass=$((pass+1)); }
bad()   { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
head_() { printf '\n== %s ==\n' "$1"; }

# mqtt_connect PORT USER PASS -> echoes the 4-byte CONNACK, or nothing.
# A CONNECT payload is assembled in Python because bash cannot carry NUL bytes
# through printf reliably, and MQTT framing is full of them.
mqtt_connect() {
  python3 - "$1" "$2" "$3" <<'PY' 2>/dev/null
import socket, struct, sys

port, user, pw = int(sys.argv[1]), sys.argv[2], sys.argv[3]

def s(x):
    return struct.pack("!H", len(x)) + x.encode()

payload = s("MQTT") + bytes([4, 0xC2]) + struct.pack("!H", 30) + s("probe-" + user)
payload += s(user) + s(pw)
packet = bytes([0x10, len(payload)]) + payload

try:
    c = socket.create_connection(("127.0.0.1", port), timeout=4)
    c.sendall(packet)
    resp = c.recv(4)
    c.close()
    # CONNACK: 20 02 <session-present> <reason>. Reason 0x00 = accepted,
    # 0x87/0x86/0x05 = not authorized / bad credentials / not authorized.
    sys.stdout.write(resp.hex())
except Exception:
    pass
PY
}

ACCEPTED="20020000"

head_ "1. Unauthenticated CONNECT must be refused"
for port in "${MQTT_PORTS[@]}"; do
  got=$(mqtt_connect "$port" "" "")
  if [ -z "$got" ] || [ "$got" != "$ACCEPTED" ]; then
    ok "port $port rejected the anonymous client (${got:-closed})"
  else
    bad "port $port ACCEPTED an unauthenticated client"
  fi
done

head_ "2. Authenticated CONNECT must succeed"
for port in "${MQTT_PORTS[@]}"; do
  got=$(mqtt_connect "$port" "$USER" "$PASS")
  if [ "$got" = "$ACCEPTED" ]; then
    ok "port $port accepted $USER with a CONNACK"
  else
    bad "port $port CONNACK=${got:-none}, want $ACCEPTED"
  fi
done

head_ "3. WebSocket port"
if timeout 8 python3 - <<'PY' 2>/dev/null
import base64, os, socket, sys
s = socket.create_connection(("127.0.0.1", 8083), timeout=5)
key = base64.b64encode(os.urandom(16)).decode()
s.send((
    "GET /mqtt HTTP/1.1\r\nHost: 127.0.0.1:8083\r\n"
    "Upgrade: websocket\r\nConnection: Upgrade\r\n"
    f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
    "Sec-WebSocket-Protocol: mqtt\r\n\r\n"
).encode())
head = s.recv(200).decode(errors="replace")
s.close()
sys.exit(0 if "101" in head.split("\r\n")[0] else 1)
PY
  then ok "port $WS_PORT completed the WebSocket upgrade"
  else bad "port $WS_PORT did not upgrade to WebSocket"
  fi

head_ "4. Management API"
code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' "$BASE/clients")
[ "$code" = "401" ] && ok "no token -> 401" || bad "no token -> $code (want 401)"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' -H 'Authorization: Bearer wrong' "$BASE/clients")
[ "$code" = "401" ] && ok "wrong token -> 401" || bad "wrong token -> $code (want 401)"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/clients")
[ "$code" = "200" ] && ok "valid token -> 200" || bad "valid token -> $code (want 200)"

body=$(curl -s -m 5 --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/clients")
grep -q '"online"' <<<"$body" && ok "/clients reports online count" \
  || bad "/clients missing online: $body"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/clients/nobody")
[ "$code" = "404" ] && ok "/clients/{unknown} -> 404" || bad "unknown client -> $code (want 404)"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' -H "Authorization: Bearer $TOKEN" "$BASE/topics")
[ "$code" = "200" ] && ok "/topics -> 200" || bad "/topics -> $code (want 200)"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' -X DELETE -H "Authorization: Bearer $TOKEN" "$BASE/clients/x")
[ "$code" = "405" ] && ok "DELETE -> 405 (read-only)" || bad "DELETE -> $code (want 405)"

head_ "5. Health"
code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' "$BASE/healthz")
[ "$code" = "200" ] && ok "/healthz -> 200" || bad "/healthz -> $code (want 200)"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' "$BASE/readyz")
[ "$code" = "200" ] && ok "/readyz -> 200" || bad "/readyz -> $code (want 200)"

code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' --noproxy '*' "$BASE/metrics")
[ "$code" = "200" ] && ok "/metrics -> 200" || bad "/metrics -> $code (want 200)"

head_ "Summary"
printf '  passed: %d\n  failed: %d\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || exit 1
printf '\nAll cloud checks passed.\n'