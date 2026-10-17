#!/usr/bin/env bash
# Smoke check: build the frontend, embed it, start the binary on a scratch database, and hit the
# UI and a few API calls the UI makes. Needs a local PostgreSQL database (SMOKE_DB_URL); the run uses a
# scratch schema in it and drops it afterwards.
set -euo pipefail
cd "$(dirname "$0")/.."
DB_URL=${SMOKE_DB_URL:-postgres://gh2test:gh2test@127.0.0.1:5432/gh2test?sslmode=disable}
SCHEMA=smoke_$$
PORT=${SMOKE_PORT:-18090}
(cd web && npm run build >/dev/null)
go build -o /tmp/gh2smoke ./cmd/app
psql "$DB_URL" -qc "CREATE SCHEMA $SCHEMA" >/dev/null
JAR=$(mktemp)
cleanup() { kill "${PID:-0}" 2>/dev/null || true; wait "${PID:-0}" 2>/dev/null || true; psql "$DB_URL" -qc "SET client_min_messages=warning; DROP SCHEMA IF EXISTS $SCHEMA CASCADE" >/dev/null; rm -f "$JAR"; }
trap cleanup EXIT
V2_ADDR=127.0.0.1:$PORT V2_DATABASE_URL="$DB_URL&search_path=$SCHEMA" V2_MASTER_KEY=0123456789abcdef0123456789abcdef \
  V2_WORKER_OFF=true V2_SERVER_IP=203.0.113.7 V2_BOOTSTRAP_ADMIN_EMAIL=root@smoke.local V2_BOOTSTRAP_ADMIN_PASSWORD=smokepass1 /tmp/gh2smoke >/tmp/gh2smoke.log 2>&1 &
PID=$!
B=http://127.0.0.1:$PORT
for _ in $(seq 50); do curl -fs "$B/api/health" >/dev/null && break; sleep 0.2; done
fail() { echo "SMOKE FAIL: $*"; tail -20 /tmp/gh2smoke.log; exit 1; }
idx=$(curl -fs "$B/")                       || fail "index"
grep -q '<div id="app">' <<<"$idx"          || fail "index content"
asset=$(grep -o '/assets/index-[^"]*\.js' <<<"$idx" | head -1)
curl -fs "$B$asset" | grep -q 'createApp\|__vue' || curl -fs -o /dev/null "$B$asset" || fail "asset $asset"
curl -fs "$B/articles" | grep -q '<div id="app">' || fail "SPA fallback"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$B/api/nope")" = 404 ] || fail "unknown api is 404"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$B/api/auth/me")" = 401 ] || fail "me without login"
# user path: register (with consent) -> login -> today, accounts, articles
curl -fs -c "$JAR" -H 'Content-Type: application/json' -d '{"email":"u@smoke.local","password":"userpass1","terms_version":"2026-10","privacy_version":"2026-10"}' "$B/api/auth/register" >/dev/null || fail register
curl -fs -c "$JAR" -H 'Content-Type: application/json' -d '{"email":"u@smoke.local","password":"userpass1"}' "$B/api/auth/login" >/dev/null || fail login
curl -fs -b "$JAR" "$B/api/me/today" | grep -q '"daily_quota"' || fail today
curl -fs -b "$JAR" "$B/api/accounts" >/dev/null || fail accounts
curl -fs -b "$JAR" "$B/api/articles" >/dev/null || fail articles
curl -fs -b "$JAR" "$B/api/site" | grep -q '203.0.113.7' || fail "server ip hint"
[ "$(curl -fs -b "$JAR" "$B/api/me/topics")" = '[]' ] || fail "pending topics"
curl -fs "$B/terms" | grep -q '<div id="app">' || fail "terms page"
[ "$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" "$B/api/admin/sensitive-words")" = 403 ] || fail "user kept out of sensitive words"
[ "$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" "$B/api/admin/users")" = 403 ] || fail "user kept out of admin"
# admin path
curl -fs -c "$JAR" -H 'Content-Type: application/json' -d '{"email":"root@smoke.local","password":"smokepass1"}' "$B/api/auth/login" >/dev/null || fail "admin login"
curl -fs -b "$JAR" "$B/api/admin/users" | grep -q 'u@smoke.local' || fail "admin users"
curl -fs -b "$JAR" "$B/api/admin/audit" >/dev/null || fail audit
curl -fs -b "$JAR" -X PUT -H 'Content-Type: application/json' -d '{"words":[" 甲词 ","乙词","甲词",""]}' "$B/api/admin/sensitive-words" >/dev/null || fail "save sensitive words"
[ "$(curl -fs -b "$JAR" "$B/api/admin/sensitive-words")" = '{"words":["乙词","甲词"]}' ] || fail "sensitive words round trip"
curl -fs -b "$JAR" -X PUT -H 'Content-Type: application/json' -d '{"auto_pause_after":3}' "$B/api/admin/settings" >/dev/null || fail "save settings"
curl -fs -b "$JAR" "$B/api/admin/settings" | grep -q '"auto_pause_after":3' || fail "settings round trip"
curl -fs -b "$JAR" "$B/api/admin/feedback" >/dev/null || fail "admin feedback"
curl -fs -b "$JAR" "$B/api/admin/routes" | grep -q '"stages"' || fail routes
echo "SMOKE OK"
