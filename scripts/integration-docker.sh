#!/usr/bin/env bash
# End-to-end test of FELDCORE's Docker module against a real Docker daemon.
#
# Requires Docker installed and running, and pulls the `alpine` image. It
# creates and removes containers named feldcore-it-*. Run on a development
# machine or a disposable VM.
#
# Usage: sudo scripts/integration-docker.sh [path/to/feldcored]

set -u

BIN=${1:-bin/feldcored}
PORT=8790
BASE="http://127.0.0.1:$PORT/api/v1"
WORK=$(mktemp -d /tmp/feldcore-dit.XXXXXX)
JAR="$WORK/cookies"
BODY="$WORK/body"
ADMIN_PASS=$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)
CNAME="feldcore-it-web"

fails=0
pass() { echo "  PASS  $1"; }
fail() { echo "  FAIL  $1"; fails=$((fails + 1)); }
check() { local d=$1; shift; if "$@" >/dev/null 2>&1; then pass "$d"; else fail "$d"; fi; }
refute() { local d=$1; shift; if "$@" >/dev/null 2>&1; then fail "$d"; else pass "$d"; fi; }

req() {
  local method=$1 path=$2 data=${3:-}
  local args=(-s -o "$BODY" -w '%{http_code}' -X "$method" -b "$JAR" -c "$JAR" --max-time 300)
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' -d "$data")
  CODE=$(curl "${args[@]}" "$BASE$path")
}
j() { python3 -c "import json;d=json.load(open('$BODY'));print($1)" 2>/dev/null; }
expect_code() { if [ "$CODE" = "$2" ]; then pass "$1"; else fail "$1 (HTTP $CODE, wanted $2: $(cat "$BODY"))"; fi; }
expect() { local got; got=$(j "$2"); if [ "$got" = "$3" ]; then pass "$1"; else fail "$1 (got '$got', wanted '$3')"; fi; }

cleanup() {
  [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
  docker rm -f "$CNAME" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

if [ "$(id -u)" -ne 0 ]; then echo "Run as root." >&2; exit 2; fi
if [ ! -x "$BIN" ]; then echo "Binary not found: $BIN" >&2; exit 2; fi
if ! docker info >/dev/null 2>&1; then echo "Docker is not running." >&2; exit 2; fi
docker rm -f "$CNAME" >/dev/null 2>&1

FELDCORE_LISTEN="127.0.0.1:$PORT" FELDCORE_DATA_DIR="$WORK/data" "$BIN" >"$WORK/daemon.log" 2>&1 &
DAEMON_PID=$!
for _ in $(seq 1 50); do curl -s "$BASE/health" >/dev/null 2>&1 && break; sleep 0.1; done

echo "== Auth gate =="
req GET /docker/status; expect_code "docker API is gated before sign-in" 401
req POST /auth/setup "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}"; expect_code "sign in" 200

echo "== Status =="
req GET /docker/status
expect "Docker reports as running" "d['running']" True
check "a version is reported" test -n "$(j "d.get('version','')")"

echo "== Validation =="
req POST /docker/containers '{"image":""}';                         expect_code "empty image refused" 400
req POST /docker/containers '{"image":"nginx","volumes":[{"host":"rel","container":"/data"}]}'
expect_code "relative bind path refused" 400
req POST /docker/containers '{"image":"nginx","ports":[{"host":70000,"container":80}]}'
expect_code "out-of-range port refused" 400

echo "== Create and run =="
req POST /docker/containers "{\"name\":\"$CNAME\",\"image\":\"alpine:latest\",\"env\":[{\"key\":\"GREETING\",\"value\":\"hi\"}],\"ports\":[{\"host\":18099,\"container\":80,\"proto\":\"tcp\"}],\"restartPolicy\":\"unless-stopped\",\"autostart\":true}" 'cmd-not-supported'
# The create body has no command, so alpine exits immediately; that is fine —
# we only need the container to exist and be managed/configured correctly.
if [ "$CODE" = "201" ]; then pass "container created via API"; else
  # alpine with no command exits; create still succeeds. A non-201 is a real failure.
  fail "container created via API (HTTP $CODE: $(cat "$BODY"))"
fi
sleep 1
req GET /docker/containers
expect "container is listed" "[c['name'] for c in d['containers'] if c['name']=='$CNAME'][0]" "$CNAME"
expect "container is marked managed by FELDCORE" "[c['managed'] for c in d['containers'] if c['name']=='$CNAME'][0]" True
expect "port mapping is reported" "[c['ports'][0]['host'] for c in d['containers'] if c['name']=='$CNAME'][0]" "18099"
check "restart policy was applied" sh -c "docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' $CNAME | grep -q unless-stopped"
check "env var was applied" sh -c "docker inspect -f '{{.Config.Env}}' $CNAME | grep -q GREETING=hi"
check "managed label was applied" sh -c "docker inspect -f '{{.Config.Labels}}' $CNAME | grep -q 'feldcore.managed:true'"
ID=$(j "[c['id'] for c in d['containers'] if c['name']=='$CNAME'][0]")

echo "== Lifecycle =="
req POST "/docker/containers/$ID/start"; expect_code "start" 200
sleep 1
req POST "/docker/containers/$ID/restart"; expect_code "restart" 200
req GET "/docker/containers/$ID/logs?tail=10"; expect_code "logs endpoint responds" 200
req POST "/docker/containers/$ID/stop"; expect_code "stop" 200
req POST "/docker/containers/does-not-exist/start"; expect_code "action on missing container is 404" 404

echo "== Images =="
req GET /docker/images
check "alpine appears in the image list" sh -c "grep -q alpine '$BODY'"

echo "== Remove =="
req DELETE "/docker/containers/$ID?force=1"; expect_code "remove" 200
sleep 1
refute "container is gone from docker" docker inspect "$CNAME"
req GET /docker/containers
expect "container is gone from the API list" "len([c for c in d['containers'] if c['name']=='$CNAME'])" "0"

echo
if [ "$fails" -eq 0 ]; then echo "ALL CHECKS PASSED"; else echo "$fails CHECK(S) FAILED"; echo "--- log ---"; tail -20 "$WORK/daemon.log"; fi
exit "$fails"
