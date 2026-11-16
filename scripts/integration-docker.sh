#!/usr/bin/env bash
# End-to-end test of FELDCORE's Docker module against a real Docker daemon.
#
# Requires Docker installed and running, and pulls `nginx:alpine`. It creates
# and removes containers named feldcore-it-* and publishes ports 18096-18099
# on localhost. Run on a development machine or a disposable VM.
#
# Usage: sudo scripts/integration-docker.sh [path/to/feldcored]

set -u

BIN=${1:-bin/feldcored}
PORT=8790
BASE="http://127.0.0.1:$PORT/api/v1"
WORK=$(mktemp -d /tmp/feldcore-dit.XXXXXX)
chmod 755 "$WORK"
JAR="$WORK/cookies"
BODY="$WORK/body"
ADMIN_PASS=$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)
IMG="nginx:alpine"
WEB="feldcore-it-web"
ALL="feldcore-it-web feldcore-it-web2 feldcore-it-blocker feldcore-it-hand feldcore-it-compose feldcore-it-web-feldcore-old feldcore-it-hand-feldcore-old"

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
cid() { docker inspect -f '{{.Id}}' "$1" 2>/dev/null; }
# serves PORT TEXT -> succeeds once http://127.0.0.1:PORT/ answers with TEXT.
# Retries for ~10s: a just-started container accepts connections a moment
# before the server inside it is ready, which shows up as a reset, not a refusal.
serves() {
  local i out
  for i in $(seq 1 20); do
    out=$(curl -s --max-time 2 "http://127.0.0.1:$1/" 2>/dev/null)
    case "$out" in *"$2"*) return 0 ;; esac
    sleep 0.5
  done
  return 1
}

cleanup() {
  [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
  for c in $ALL; do docker rm -f "$c" >/dev/null 2>&1; done
  rm -rf "$WORK"
}
trap cleanup EXIT

if [ "$(id -u)" -ne 0 ]; then echo "Run as root." >&2; exit 2; fi
if [ ! -x "$BIN" ]; then echo "Binary not found: $BIN" >&2; exit 2; fi
if ! docker info >/dev/null 2>&1; then echo "Docker is not running." >&2; exit 2; fi
for c in $ALL; do docker rm -f "$c" >/dev/null 2>&1; done
docker pull -q "$IMG" >/dev/null 2>&1

mkdir -p "$WORK/site" "$WORK/extra"
echo "version-one" >"$WORK/site/index.html"

FELDCORE_LISTEN="127.0.0.1:$PORT" FELDCORE_DATA_DIR="$WORK/data" "$BIN" >"$WORK/daemon.log" 2>&1 &
DAEMON_PID=$!
for _ in $(seq 1 50); do curl -s "$BASE/health" >/dev/null 2>&1 && break; sleep 0.1; done

# web_spec HOSTPORT [EXTRA_JSON_FIELDS] [EXTRA_CONFIGS]
web_spec() {
  cat <<EOF
{"name":"${4:-$WEB}","image":"$IMG","network":"bridge","restartPolicy":"unless-stopped","autostart":true${2:-},
 "configs":[
  {"type":"path","name":"Site","description":"Static files","host":"$WORK/site","container":"/usr/share/nginx/html","mode":"ro"},
  {"type":"port","name":"Web UI","hostPort":$1,"containerPort":80,"proto":"tcp"},
  {"type":"variable","name":"Greeting","key":"GREETING","value":"hi"},
  {"type":"label","key":"com.example.role","value":"frontend"}${3:-}
 ]}
EOF
}

echo "== Auth gate =="
req GET /docker/status;        expect_code "docker API is gated before sign-in" 401
req GET "/fs/suggest?q=/";     expect_code "folder suggestions are gated before sign-in" 401
req POST /auth/setup "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}"; expect_code "sign in" 200

echo "== Status and options =="
req GET /docker/status;  expect "Docker reports as running" "d['running']" True
req GET /docker/options
expect "bridge network is offered" "'bridge' in d['networks']" True
GPU_OK=$(j "d['gpuAvailable']")
echo "        (GPU shareable on this machine: $GPU_OK; $(j "[g['name'] for g in d['gpus']]"))"

echo "== Folder suggestions =="
req GET "/fs/suggest?q=";             expect_code "empty query answers" 200
req GET "/fs/suggest?q=/";            expect "typing / lists root folders" "'etc' in [x['name'] for x in d['dirs']]" True
req GET "/fs/suggest?q=/us";          expect "a partial name narrows the list" "[x['path'] for x in d['dirs']]" "['/usr']"
req GET "/fs/suggest?q=$WORK/";       expect "lists folders inside a folder" "{'extra', 'site'} <= {x['name'] for x in d['dirs']}" True
expect "never lists files" "'index.html' in str(d)" False

echo "== Validation =="
req POST /docker/containers '{"image":""}';  expect_code "empty image refused" 400
# (A host value with no slash at all is a named Docker volume, which is valid.)
req POST /docker/containers "{\"image\":\"$IMG\",\"configs\":[{\"type\":\"path\",\"host\":\"rel/path\",\"container\":\"/data\"}]}"
expect_code "relative host path refused" 400
req POST /docker/containers "{\"image\":\"$IMG\",\"configs\":[{\"type\":\"port\",\"hostPort\":70000,\"containerPort\":80}]}"
expect_code "out-of-range port refused" 400
req POST /docker/containers "{\"image\":\"$IMG\",\"configs\":[{\"type\":\"path\",\"host\":\"/a\",\"container\":\"/b\",\"mode\":\"rw:/etc:/x\"}]}"
expect_code "bind option injection refused" 400
req POST /docker/containers "{\"image\":\"$IMG\",\"configs\":[{\"type\":\"device\",\"host\":\"/etc/shadow\"}]}"
expect_code "device outside /dev refused" 400

echo "== Create =="
req POST /docker/containers "$(web_spec 18099)"; expect_code "container created with path, port, variable and label" 201
check "it serves the mapped folder on the mapped port" serves 18099 version-one
check "restart policy applied" sh -c "docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' $WEB | grep -qx unless-stopped"
check "variable applied" sh -c "docker inspect -f '{{.Config.Env}}' $WEB | grep -q GREETING=hi"
check "path mapped read-only" sh -c "docker inspect -f '{{.HostConfig.Binds}}' $WEB | grep -q ':/usr/share/nginx/html:ro'"
req GET /docker/containers
expect "listed as managed" "[c['managed'] for c in d['containers'] if c['name']=='$WEB'][0]" True
expect "allocation shows the bridge network" "[c['network'] for c in d['containers'] if c['name']=='$WEB'][0]" bridge
expect "allocation shows the host port" "[c['ports'][0]['host'] for c in d['containers'] if c['name']=='$WEB'][0]" 18099

echo "== Read settings back =="
req GET "/docker/containers/$WEB/spec"; expect_code "spec endpoint responds" 200
expect "editable" "d['editable']" True
expect "names are remembered" "sorted(c['name'] for c in d['spec']['configs'] if c['name'])" "['Greeting', 'Site', 'Web UI']"
expect "descriptions are remembered" "[c['description'] for c in d['spec']['configs'] if c['type']=='path'][0]" "Static files"
expect "image defaults are not shown as settings" "[c['key'] for c in d['spec']['configs'] if c['type']=='variable']" "['GREETING']"
expect "internal labels are hidden" "[c['key'] for c in d['spec']['configs'] if c['type']=='label']" "['com.example.role']"

echo "== Edit (recreate) =="
OLD_ID=$(cid $WEB)
echo "version-two" >"$WORK/site/index.html"   # data lives in the mapped folder
req PUT "/docker/containers/$WEB" "$(web_spec 18098 '' ',{"type":"variable","key":"EXTRA","value":"1"}')"
expect_code "edit accepted" 200
NEW_ID=$(cid $WEB)
check "the container was replaced" test -n "$NEW_ID" -a "$NEW_ID" != "$OLD_ID"
check "it is running again" sh -c "docker inspect -f '{{.State.Running}}' $WEB | grep -qx true"
check "new port serves, with the folder's data intact" serves 18098 version-two
refute "old port is released" curl -s --max-time 2 http://127.0.0.1:18099/
check "new variable applied, old one kept" sh -c "docker inspect -f '{{.Config.Env}}' $WEB | grep -q EXTRA=1 && docker inspect -f '{{.Config.Env}}' $WEB | grep -q GREETING=hi"
check "image default env still present" sh -c "docker inspect -f '{{.Config.Env}}' $WEB | grep -q NGINX_VERSION"
check "restart policy survived" sh -c "docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' $WEB | grep -qx unless-stopped"
refute "no leftover of the old container" docker inspect "$WEB-feldcore-old"
req GET "/docker/containers/$WEB/spec"
expect "names survive an edit" "sorted(c['name'] for c in d['spec']['configs'] if c['name'])" "['Greeting', 'Site', 'Web UI']"

echo "== Failed edits roll back =="
docker run -d --name feldcore-it-blocker -p 18097:80 "$IMG" >/dev/null
BEFORE=$(cid $WEB)
req PUT "/docker/containers/$WEB" "$(web_spec 18097)"
expect_code "edit onto a port already in use is refused" 500
check "the error says the previous container was restored" grep -qi "restored" "$BODY"
check "the original container is back, untouched" test "$(cid $WEB)" = "$BEFORE"
check "and running" sh -c "docker inspect -f '{{.State.Running}}' $WEB | grep -qx true"
check "and still serving on its port" serves 18098 version-two
refute "no leftover after rollback" docker inspect "$WEB-feldcore-old"
refute "no half-made replacement left" sh -c "docker ps -a --format '{{.Names}}' | grep -v '^$WEB\$' | grep -q '^$WEB'"
req PUT "/docker/containers/$WEB" "$(web_spec 18098 | sed "s|\"image\":\"$IMG\"|\"image\":\"feldcore/does-not-exist:nope\"|")"
expect_code "edit to a missing image is refused" 400
check "container untouched by a failed image pull" test "$(cid $WEB)" = "$BEFORE"
docker rm -f feldcore-it-blocker >/dev/null 2>&1

echo "== GPU =="
if [ "$GPU_OK" = "True" ]; then
  req PUT "/docker/containers/$WEB" "$(web_spec 18098 ',"gpu":true')"; expect_code "GPU switched on" 200
  check "GPU device passed through" sh -c "docker inspect -f '{{.HostConfig.Devices}}' $WEB | grep -q /dev/dri"
  check "GPU groups added" sh -c "docker inspect -f '{{.HostConfig.GroupAdd}}' $WEB | grep -q '[0-9]'"
  check "container can see the render device" sh -c "docker exec $WEB ls /dev/dri | grep -q renderD"
  req GET "/docker/containers/$WEB/spec"
  expect "shown as the GPU switch" "d['spec']['gpu']" True
  expect "not shown as a raw device" "[c for c in d['spec']['configs'] if c['type']=='device']" "[]"
  req GET /docker/containers
  expect "listed with the GPU flag" "[c['gpu'] for c in d['containers'] if c['name']=='$WEB'][0]" True
  req PUT "/docker/containers/$WEB" "$(web_spec 18098)"; expect_code "GPU switched off" 200
  refute "GPU device removed" sh -c "docker inspect -f '{{.HostConfig.Devices}}' $WEB | grep -q /dev/dri"
  check "GPU groups removed" sh -c "docker inspect -f '{{.HostConfig.GroupAdd}}' $WEB | grep -qx '\[\]'"
else
  echo "        (skipped: no shareable GPU here)"
  req POST /docker/containers "{\"image\":\"$IMG\",\"gpu\":true}"; expect_code "GPU request without a GPU is refused" 400
fi

echo "== Editing a container made outside FELDCORE keeps its other settings =="
docker run -d --name feldcore-it-hand --cap-add NET_ADMIN --memory 256m --hostname custom-host \
  -e FOO=bar "$IMG" nginx-debug -g 'daemon off;' >/dev/null
req GET /docker/containers/feldcore-it-hand/spec
expect "its variable is read" "[c['value'] for c in d['spec']['configs'] if c['key']=='FOO']" "['bar']"
req PUT /docker/containers/feldcore-it-hand "{\"name\":\"feldcore-it-hand\",\"image\":\"$IMG\",\"configs\":[{\"type\":\"variable\",\"key\":\"FOO\",\"value\":\"bar\"},{\"type\":\"port\",\"hostPort\":18096,\"containerPort\":80}]}"
expect_code "edited" 200
check "the new port works" serves 18096 nginx
check "capability kept" sh -c "docker inspect -f '{{.HostConfig.CapAdd}}' feldcore-it-hand | grep -q NET_ADMIN"
check "memory limit kept" sh -c "docker inspect -f '{{.HostConfig.Memory}}' feldcore-it-hand | grep -qx 268435456"
check "custom hostname kept" sh -c "docker inspect -f '{{.Config.Hostname}}' feldcore-it-hand | grep -qx custom-host"
check "custom command kept" sh -c "docker inspect -f '{{.Config.Cmd}}' feldcore-it-hand | grep -q nginx-debug"
check "image entrypoint not pinned" sh -c "docker inspect -f '{{json .Config.Entrypoint}}' feldcore-it-hand | grep -q docker-entrypoint"

echo "== Compose containers are left alone =="
docker run -d --name feldcore-it-compose -l com.docker.compose.project=demo "$IMG" >/dev/null
COMPOSE_ID=$(cid feldcore-it-compose)
req GET /docker/containers/feldcore-it-compose/spec; expect "reported as not editable" "d['editable']" False
req PUT /docker/containers/feldcore-it-compose "{\"image\":\"$IMG\"}"; expect_code "edit refused" 400
check "container untouched" test "$(cid feldcore-it-compose)" = "$COMPOSE_ID"

echo "== Rename through edit =="
req PUT "/docker/containers/$WEB" "$(web_spec 18098 '' '' feldcore-it-web2)"; expect_code "renamed" 200
refute "old name is gone" docker inspect "$WEB"
req GET /docker/containers/feldcore-it-web2/spec
expect "names follow the rename" "sorted(c['name'] for c in d['spec']['configs'] if c['name'])" "['Greeting', 'Site', 'Web UI']"

echo "== Lifecycle and removal =="
req POST /docker/containers/feldcore-it-web2/stop;    expect_code "stop" 200
req GET /docker/containers
expect "a stopped container still lists cleanly" "[type(c['ports']).__name__ + type(c['ips']).__name__ for c in d['containers'] if c['name']=='feldcore-it-web2'][0]" listlist
expect "a stopped container still shows the port it is set to use" "[c['ports'][0]['host'] for c in d['containers'] if c['name']=='feldcore-it-web2'][0]" 18098
req POST /docker/containers/feldcore-it-web2/start;   expect_code "start" 200
req GET "/docker/containers/feldcore-it-web2/logs?tail=5"; expect_code "logs" 200
req POST /docker/containers/does-not-exist/start;     expect_code "action on a missing container is 404" 404
req DELETE "/docker/containers/feldcore-it-web2?force=1"; expect_code "remove" 200
refute "container is gone" docker inspect feldcore-it-web2
refute "its stored names are forgotten" grep -q feldcore-it-web "$WORK/data/state.json"
check "files in the mapped folder are untouched" grep -qx version-two "$WORK/site/index.html"

echo
if [ "$fails" -eq 0 ]; then echo "ALL CHECKS PASSED"; else echo "$fails CHECK(S) FAILED"; echo "--- log ---"; tail -25 "$WORK/daemon.log"; fi
exit "$fails"
