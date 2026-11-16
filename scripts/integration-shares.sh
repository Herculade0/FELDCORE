#!/usr/bin/env bash
# End-to-end test of FELDCORE shares against real Samba and NFS.
#
# WARNING: this changes the machine it runs on. It installs samba, smbclient
# and nfs-kernel-server, adds an include line to /etc/samba/smb.conf, and
# creates then deletes a unix user. Run it as root on a development machine or
# a disposable VM, never on a server you care about.
#
# Usage: sudo scripts/integration-shares.sh [path/to/feldcored]

set -u

BIN=${1:-bin/feldcored}
PORT=8788
BASE="http://127.0.0.1:$PORT/api/v1"
WORK=$(mktemp -d /tmp/feldcore-it.XXXXXX)
# mktemp makes this private to root; Samba serves files as "nobody", which
# must be able to walk down to the share folders.
chmod 755 "$WORK"
JAR="$WORK/cookies"
BODY="$WORK/body"
ROOT="$WORK/shares"
# Throwaway credentials, generated per run and never printed.
ADMIN_PASS=$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)
USER_PASS=$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)
SHARE_USER="feldcoreit"

fails=0
pass() { echo "  PASS  $1"; }
fail() { echo "  FAIL  $1"; fails=$((fails + 1)); }
check() { local d=$1; shift; if "$@" >/dev/null 2>&1; then pass "$d"; else fail "$d"; fi; }
refute() { local d=$1; shift; if "$@" >/dev/null 2>&1; then fail "$d"; else pass "$d"; fi; }

# req METHOD PATH [JSON] [extra curl args...] -> sets CODE, writes $BODY
req() {
  local method=$1 path=$2 data=${3:-}
  shift; shift; [ $# -gt 0 ] && shift
  local args=(-s -o "$BODY" -w '%{http_code}' -X "$method" -b "$JAR" -c "$JAR" --max-time 900)
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' -d "$data")
  CODE=$(curl "${args[@]}" "$@" "$BASE$path")
}
# j EXPR -> evaluate a Python expression against the last JSON body as `d`
j() { python3 -c "import json;d=json.load(open('$BODY'));print($1)" 2>/dev/null; }
expect_code() { if [ "$CODE" = "$2" ]; then pass "$1"; else fail "$1 (HTTP $CODE, wanted $2: $(cat "$BODY"))"; fi; }
expect() { local got; got=$(j "$2"); if [ "$got" = "$3" ]; then pass "$1"; else fail "$1 (got '$got', wanted '$3')"; fi; }

cleanup() {
  [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
  mountpoint -q "$WORK/mnt" 2>/dev/null && umount "$WORK/mnt"
  id "$SHARE_USER" >/dev/null 2>&1 && { smbpasswd -x "$SHARE_USER" >/dev/null 2>&1; userdel "$SHARE_USER"; }
  rm -rf "$WORK"
}
trap cleanup EXIT

if [ "$(id -u)" -ne 0 ]; then echo "Run as root." >&2; exit 2; fi
if [ ! -x "$BIN" ]; then echo "Binary not found: $BIN (run: make build)" >&2; exit 2; fi

FELDCORE_LISTEN="127.0.0.1:$PORT" FELDCORE_DATA_DIR="$WORK/data" FELDCORE_SHARES_ROOT="$ROOT" \
  "$BIN" >"$WORK/daemon.log" 2>&1 &
DAEMON_PID=$!
for _ in $(seq 1 50); do curl -s "$BASE/health" >/dev/null 2>&1 && break; sleep 0.1; done

echo "== Login gate =="
req GET /auth/status;                 expect "fresh install asks for setup" "d['setupRequired']" True
req GET /shares;                      expect_code "API refuses requests before sign-in" 401
req GET /system/stats;                expect_code "metrics are gated too" 401
req POST /auth/setup '{"username":"admin","password":"short"}'
expect_code "weak password is refused" 400
req POST /auth/setup "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}"
expect_code "administrator account is created" 200
req POST /auth/setup "{\"username\":\"evil\",\"password\":\"$ADMIN_PASS\"}"
expect_code "setup cannot be run twice" 409
req GET /auth/status;                 expect "session is signed in" "d['authenticated']" True
req POST /shares '{"name":"x"}' -H 'Origin: http://evil.example'
expect_code "cross-site write is refused" 403
check "state file is private to root" test "$(stat -c %a "$WORK/data/state.json")" = 600
refute "password is not stored in clear text" grep -q "$ADMIN_PASS" "$WORK/data/state.json"

echo "== Shares before Samba is installed =="
req POST /shares '{"name":"ittest","comment":"integration","smb":{"enabled":true,"access":"public"}}'
expect_code "share is created" 201
check "share folder exists" test -d "$ROOT/ittest"
check "share folder is owned by nobody" test "$(stat -c %U "$ROOT/ittest")" = nobody
req POST /shares '{"name":"../etc","smb":{"enabled":false}}'
expect_code "path traversal in a name is refused" 400
req POST /shares '{"name":"ITTEST"}';  expect_code "duplicate name is refused" 400

echo "== Samba =="
if command -v smbd >/dev/null 2>&1; then
  echo "  (samba already installed; skipping the install test)"
else
  req GET /shares; expect "share reports why it is not live" "d['shares'][0]['smbStatus']['state']" error
  req POST /shares/services/smb/install
  expect_code "Samba installs through FELDCORE" 200
fi
DEBIAN_FRONTEND=noninteractive apt-get install -y smbclient >/dev/null 2>&1
sleep 2
req GET /shares
expect "Samba is running" "d['services']['smb']['active']" True
expect "share is live over SMB" "d['shares'][0]['smbStatus']['state']" active
check "smb.conf includes the FELDCORE file" grep -q "include = /etc/samba/feldcore-shares.conf" /etc/samba/smb.conf
check "original smb.conf was backed up" test -f /etc/samba/smb.conf.feldcore-backup
check "testparm accepts the configuration" testparm -s

echo "hello from feldcore" >"$WORK/hello.txt"
check "guest can list the public share" smbclient -N //127.0.0.1/ittest -c 'ls'
check "guest can write to the public share" smbclient -N //127.0.0.1/ittest -c "put $WORK/hello.txt hello.txt"
check "the file landed in the share folder" grep -q "hello from feldcore" "$ROOT/ittest/hello.txt"
check "the file is owned by nobody" test "$(stat -c %U "$ROOT/ittest/hello.txt")" = nobody

req PUT /shares/ittest '{"comment":"integration","smb":{"enabled":true,"access":"readonly"}}'
expect_code "share switches to read-only" 200
sleep 1
check "guest can still read" smbclient -N //127.0.0.1/ittest -c "get hello.txt $WORK/back.txt"
refute "guest can no longer write" smbclient -N //127.0.0.1/ittest -c "put $WORK/hello.txt denied.txt"

echo "== Private share and share users =="
req POST /shares/users '{"name":"root","password":"'"$USER_PASS"'"}'
expect_code "an existing system account cannot be taken over" 400
req POST /shares/users "{\"name\":\"$SHARE_USER\",\"password\":\"$USER_PASS\"}"
expect_code "share user is created" 201
check "unix account exists with no login shell" test "$(getent passwd "$SHARE_USER" | cut -d: -f7)" = /usr/sbin/nologin
req POST /shares "{\"name\":\"itpriv\",\"smb\":{\"enabled\":true,\"access\":\"private\",\"users\":[\"$SHARE_USER\"]}}"
expect_code "private share is created" 201
sleep 1
refute "guest is refused on the private share" smbclient -N //127.0.0.1/itpriv -c 'ls'
refute "wrong password is refused" smbclient //127.0.0.1/itpriv -U "$SHARE_USER%wrong-password" -c 'ls'
check "the share user can write" smbclient //127.0.0.1/itpriv -U "$SHARE_USER%$USER_PASS" -c "put $WORK/hello.txt secret.txt"
req DELETE "/shares/users/$SHARE_USER"
expect_code "a user still in use cannot be deleted" 400

echo "== NFS =="
if ! command -v exportfs >/dev/null 2>&1; then
  req POST /shares/services/nfs/install
  expect_code "NFS server installs through FELDCORE" 200
fi
req PUT /shares/ittest '{"comment":"integration","smb":{"enabled":true,"access":"readonly"},"nfs":{"enabled":true,"clients":"127.0.0.1"}}'
expect_code "NFS export is enabled" 200
sleep 2
req GET /shares
NFS_STATE=$(j "[s for s in d['shares'] if s['name']=='ittest'][0]['nfsStatus']")
if exportfs -v 2>/dev/null | grep -q "$ROOT/ittest"; then
  pass "kernel is exporting the share"
  mkdir -p "$WORK/mnt"
  if mount -t nfs -o vers=4,timeo=50,retrans=1 "127.0.0.1:$ROOT/ittest" "$WORK/mnt" 2>"$WORK/mount.err"; then
    check "NFS client can read the file written over SMB" grep -q "hello from feldcore" "$WORK/mnt/hello.txt"
    check "NFS client can write" sh -c "echo nfs > '$WORK/mnt/from-nfs.txt'"
    check "NFS writes are squashed to nobody" test "$(stat -c %U "$ROOT/ittest/from-nfs.txt")" = nobody
    umount "$WORK/mnt"
  else
    fail "NFS mount ($(tr '\n' ' ' <"$WORK/mount.err"))"
  fi
else
  fail "kernel is not exporting the share (status: $NFS_STATE)"
fi

echo "== Teardown =="
req DELETE /shares/itpriv;            expect_code "private share is deleted" 200
expect "folder with files is kept" "d['folderKept']" True
check "its files are untouched" test -f "$ROOT/itpriv/secret.txt"
req DELETE "/shares/$SHARE_USER-nope"; expect_code "deleting a missing share is a 404" 404
req DELETE "/shares/users/$SHARE_USER"; expect_code "unused share user is deleted" 200
refute "unix account is gone" id "$SHARE_USER"
req DELETE /shares/ittest;            expect_code "public share is deleted" 200
sleep 1
refute "Samba config has no shares left" grep -q '^\[' /etc/samba/feldcore-shares.conf
refute "share is gone from Samba" smbclient -N //127.0.0.1/ittest -c 'ls'
refute "exports file has no shares left" grep -q "$ROOT" /etc/exports.d/feldcore.exports
check "testparm still accepts the configuration" testparm -s
req POST /auth/logout;                expect_code "sign out" 200
req GET /shares;                      expect_code "API is gated again after sign-out" 401

echo
if [ "$fails" -eq 0 ]; then echo "ALL CHECKS PASSED"; else echo "$fails CHECK(S) FAILED"; echo "--- daemon log ---"; tail -25 "$WORK/daemon.log"; fi
exit "$fails"
