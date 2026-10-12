#!/usr/bin/env bash
# Roadmap 12's live gate (RIZ-25): every API arm of live-gate-backlog.md §7c that
# needs no browser, no worker, no model and no second psql session, against a
# freshly seeded local stack. One line per arm, `PASS <arm>` or `FAIL <arm> —
# what came back`, and a non-zero exit if any arm failed.
#
#   ./arms.sh http://localhost:8080 "$ADMIN_TOKEN" "$MEMBER_TOKEN"
#
# The two tokens are admin A's and member M's from seed.sql (README.md shows
# the login). Both are 15-minute JWTs; a full run takes a few minutes, most of it
# waiting out the API's own rate limit (60 requests a minute per company).
#
# **Run it on a fresh seed, and with no model-carrying worker on the stack's
# Redis.** It restricts and grants as it goes, and it enqueues a handful of chat
# turns — accepted (202) is all an arm needs, and a worker holding a model key
# would spend money answering them. It ends with every resource open again, but
# seeding again is the reset, not this script.
#
# Database checks (rows revoked, view counts, threads written) read the stack's
# Postgres through psql and the usual PG* variables. Set NO_DB=1 to skip them;
# set METRICS_TOKEN to the API's to check the refusal counter as well.
#
# **It refuses any API or database that is not on this machine**, because every
# arm writes: it restricts, grants, revokes links and mints keys. FORCE=1 is the
# override, and there is no reason to use it.

set -uo pipefail

API=${1:?usage: arms.sh <api-url> <admin-token> <member-token>}
TA=${2:?admin token required}
TM=${3:?member token required}
API=${API%/}

case "$API" in
  http://localhost:* | http://127.0.0.1:* | http://\[::1\]:*) ;;
  *)
    if [ "${FORCE:-}" != 1 ]; then
      echo "refusing $API: arms.sh writes, and only runs against this machine (FORCE=1 overrides)" >&2
      exit 2
    fi
    ;;
esac
case "${PGHOST:-localhost}" in
  localhost | 127.0.0.1 | ::1 | /*) ;;
  *)
    if [ "${FORCE:-}" != 1 ] && [ "${NO_DB:-}" != 1 ]; then
      echo "refusing PGHOST=$PGHOST: the database checks only run against this machine (NO_DB=1 skips them)" >&2
      exit 2
    fi
    ;;
esac
for tool in curl jq openssl; do
  command -v "$tool" >/dev/null || { echo "arms.sh needs $tool" >&2; exit 2; }
done
if [ "${NO_DB:-}" != 1 ]; then
  command -v psql >/dev/null || { echo "arms.sh needs psql (or NO_DB=1)" >&2; exit 2; }
fi

# ---------------------------------------------------------------------------
# The seed's ids. Readable on purpose; seed.sql explains the scheme.
# ---------------------------------------------------------------------------
CO_A=a0000000-0000-4000-8000-000000000001
U_A=a0000000-0001-4000-8000-000000000001
U_M=a0000000-0001-4000-8000-000000000002
U_B=b0000000-0001-4000-8000-000000000001
HR=a0000000-0002-4000-8000-000000000001
FIN=a0000000-0002-4000-8000-000000000002
OPS=a0000000-0002-4000-8000-000000000003
PAYROLL=a0000000-0003-4000-8000-000000000001
SALES=a0000000-0003-4000-8000-000000000002
SCRATCH=a0000000-0003-4000-8000-000000000004
B_DASH=b0000000-0003-4000-8000-000000000001
SHARE_LIVE1=a0000000-0004-4000-8000-000000000001
SHARE_LIVE2=a0000000-0004-4000-8000-000000000002
SHARE_REVOKED=a0000000-0004-4000-8000-000000000003
SHARE_EXPIRED=a0000000-0004-4000-8000-000000000004
HR_SRC=a0000000-0005-4000-8000-000000000001
PAYROLL_PDF=a0000000-0006-4000-8000-000000000001
HANDBOOK_PDF=a0000000-0006-4000-8000-000000000002
PAYROLL_TABLE=a0000000-0007-4000-8000-000000000001
T_HR=a0000000-0008-4000-8000-000000000001
T_ROOM=a0000000-0008-4000-8000-000000000002
T_REMOVED=a0000000-0008-4000-8000-000000000003
T_UNPINNED=a0000000-0008-4000-8000-000000000004
T_RETIRED=a0000000-0008-4000-8000-000000000005
T_FIN=a0000000-0008-4000-8000-000000000006
DOC_HR=a0000000-000a-4000-8000-000000000001
DOC_FIN=a0000000-000a-4000-8000-000000000002
DOC_RENDER=a0000000-000a-4000-8000-000000000003
DOC_HR_SLIDES=a0000000-000a-4000-8000-000000000004
LINK_HR=a0000000-000b-4000-8000-000000000001
LINK_HR2=a0000000-000b-4000-8000-000000000002
PROP_HR=a0000000-000c-4000-8000-000000000001
PROP_NONE=a0000000-000c-4000-8000-000000000002
BINDING_HR=a0000000-000e-4000-8000-000000000001
# Well-formed and nobody's: the "unknown id" every byte-identical arm compares to.
NOBODY=00000000-0000-4000-8000-0000000000ff

PASSES=0
FAILS=0
SKIPS=0

# ---------------------------------------------------------------------------
# Plumbing.
# ---------------------------------------------------------------------------

# call METHOD PATH TOKEN [BODY] — sets CODE and BODY. Retries on 429: the API
# allows sixty requests a minute per company, and this script makes more.
call() {
  local method=$1 path=$2 token=$3 body=${4:-} tmp try
  tmp=$(mktemp)
  for try in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if [ -n "$body" ]; then
      CODE=$(curl -s -o "$tmp" -w '%{http_code}' -X "$method" \
        -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
        ${EXTRA_HEADER:+-H "$EXTRA_HEADER"} -d "$body" "$API$path")
    else
      CODE=$(curl -s -o "$tmp" -w '%{http_code}' -X "$method" \
        -H "Authorization: Bearer $token" \
        ${EXTRA_HEADER:+-H "$EXTRA_HEADER"} "$API$path")
    fi
    [ "$CODE" = 429 ] || break
    sleep 2
  done
  BODY=$(cat "$tmp")
  rm -f "$tmp"
}

# anon PATH — a keyless GET (the share pages), same CODE/BODY contract.
anon() {
  local tmp
  tmp=$(mktemp)
  CODE=$(curl -s -o "$tmp" -w '%{http_code}' "$API$1")
  BODY=$(cat "$tmp")
  rm -f "$tmp"
}

pass() { PASSES=$((PASSES + 1)); echo "PASS $1"; }
fail() { FAILS=$((FAILS + 1)); echo "FAIL $1 — $2"; }
skip() { SKIPS=$((SKIPS + 1)); echo "SKIP $1 — $2"; }

# expect ARM CODE [JQ] — the last call answered CODE, and JQ (if given) is true
# of its body.
expect() {
  local arm=$1 want=$2 filter=${3:-}
  if [ "$CODE" != "$want" ]; then
    fail "$arm" "want $want, got $CODE ${BODY:0:200}"
    return
  fi
  if [ -n "$filter" ] && ! printf '%s' "$BODY" | jq -e "$filter" >/dev/null 2>&1; then
    fail "$arm" "$CODE but body does not satisfy $filter: ${BODY:0:200}"
    return
  fi
  pass "$arm"
}

# same ARM A B — two bodies are byte-identical.
same() {
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1" "differ: ${2:0:120} | ${3:0:120}"; fi
}

# sql QUERY — one value from the stack's database, or empty under NO_DB.
sql() {
  [ "${NO_DB:-}" = 1 ] && return 0
  psql -X -At -v ON_ERROR_STOP=1 -c "$1" 2>&1
}

# dbcheck ARM QUERY WANT — a database fact, skipped under NO_DB.
dbcheck() {
  if [ "${NO_DB:-}" = 1 ]; then skip "$1" "NO_DB=1"; return; fi
  local got
  got=$(sql "$2")
  if [ "$got" = "$3" ]; then pass "$1"; else fail "$1" "want '$3', got '$got'"; fi
}

# metric SERIES — the refusal counter's value from /metrics, 0 when absent.
metric() {
  local series value
  while read -r series value; do
    if [ "$series" = "$1" ]; then echo "${value%.*}"; return; fi
  done < <(curl -s -H "Authorization: Bearer $METRICS_TOKEN" "$API/metrics")
  echo 0
}

mode() { call PUT "/api/access/$1/$2/mode" "$TA" "{\"access_mode\":\"$3\"}"; }
grant() { call PUT "/api/access/$1/$2/grants/$3" "$TA"; }
revoke() { call DELETE "/api/access/$1/$2/grants/$3" "$TA"; }

refused_rows() {
  sql "SELECT count(*) FROM agent_actions WHERE company_id = '$CO_A' AND tool_name = 'access.refused'"
}

echo "# arms.sh against $API"

# ===========================================================================
# Q14 / Q15 — everything open: nothing is hidden, and the empty states.
# ===========================================================================
call GET /api/agents "$TM"
expect "Q14 M lists all three agents, all reachable" 200 \
  '([.agents[].id] | sort) == (["'"$HR"'","'"$FIN"'","'"$OPS"'"] | sort) and (.reachable_agent_ids | length) == 3'
call GET /api/dashboards "$TM"
expect "Q14 M lists all four dashboards" 200 '(.dashboards | length) == 4'
call GET /api/connections "$TM"
expect "Q14 M lists both sources" 200 '(.connections // . | length) == 2'
call GET /api/knowledge/documents "$TM"
expect "Q14 M lists both uploaded documents" 200 '(.documents | length) == 2'
call GET /api/documents "$TM"
expect "Q14 M lists all four generated documents" 200 '(.documents | length) == 4'
# §19e: a download URL handed out while HR is open, kept for after the restriction.
call GET /api/documents "$TA"
HR_DECK_URL=$(printf '%s' "$BODY" | jq -r '.documents[] | select(.id == "'"$DOC_HR"'") | .download_url // empty')
call GET /api/actions/pending "$TM"
expect "Q14 M lists both proposals" 200 '(.actions // .invocations // .pending | length) == 2'
call GET /api/threads "$TM"
expect "Q14 M lists all six conversations" 200 '(.threads | length) == 6'

call GET /api/access/agent "$TA"
expect "Q15/T-Z7 GET /api/access/agent: all three, open, an agent nobody holds still listed with []" 200 \
  '(.resources | length) == 3 and all(.resources[]; .access_mode == "open" and .grants == [])'
call GET "/api/users/$U_M/capabilities" "$TA"
expect "Q15 M's capabilities with nothing held → []" 200 '(.capabilities // .) == []'
call GET "/api/users/$U_M/grants" "$TA"
expect "Q15 M's grants with nothing held → []" 200 '.grants == []'
mode dashboard "$SCRATCH" restricted
expect "Q15 restrict a dashboard with no links → {\"revoked_shares\":0}" 200 '.revoked_shares == 0'
mode dashboard "$SCRATCH" open
expect "Q15 re-open it" 200

# ===========================================================================
# T-Z5/T-Z6 — the access read's name expressions, against real rows.
# ===========================================================================
call GET /api/access/dashboard "$TA"
expect "T-Z5 /api/access/dashboard names Payroll by title" 200 \
  '[.resources[] | select(.resource_id == "'"$PAYROLL"'") | .name] == ["Payroll"]'
call GET /api/access/connection "$TA"
expect "T-Z6 /api/access/connection: a label, and a nameless source read as its engine" 200 \
  '([.resources[].name] | sort) == ["HR warehouse","postgres"]'
call GET /api/access/document "$TA"
expect "T-Z6 /api/access/document names Payroll 2026.pdf by filename" 200 \
  '[.resources[] | select(.resource_id == "'"$PAYROLL_PDF"'") | .name] == ["Payroll 2026.pdf"]'

# ===========================================================================
# T-Z1 — capabilities, by curl, as two people.
# ===========================================================================
call PUT "/api/users/$U_M/capabilities/voice" "$TA"
expect "T-Z1 A grants voice to M" 204
call PUT "/api/users/$U_M/capabilities/voice" "$TA"
expect "T-Z1 granting again is not a conflict" 204
dbcheck "T-Z1 granted twice is one row, granted_by A" \
  "SELECT count(*) || ':' || max(granted_by::text) FROM user_capabilities WHERE user_id = '$U_M' AND capability = 'voice'" \
  "1:$U_A"
call GET /api/users/me/capabilities "$TM"
expect "T-Z1 M reads voice on their next request" 200 '[.capabilities[] | (.capability // .)] | index("voice") != null'
call PUT "/api/users/$U_B/capabilities/voice" "$TA"
expect "T-Z1 a grant to company B's user is not found" 404
dbcheck "T-Z1 …and writes no row" \
  "SELECT count(*) FROM user_capabilities WHERE user_id = '$U_B'" "0"
call GET "/api/users/$U_B/capabilities" "$TA"
expect "T-Z1 listing company B's user is not found" 404
call DELETE "/api/users/$U_M/capabilities/voice" "$TA"
expect "T-Z1 A revokes voice" 204
call GET /api/users/me/capabilities "$TM"
expect "T-Z1 M's very next read is [], no re-login" 200 '.capabilities == []'
call DELETE "/api/users/$U_M/capabilities/voice" "$TA"
expect "T-Z1 revoking what is not held succeeds" 204

# ===========================================================================
# T-Z2 — the resource statements, through their routes.
# ===========================================================================
grant agent "$HR" "$U_M"
expect "T-Z2 grant HR to M" 204
grant agent "$HR" "$U_M"
expect "T-Z2 grant again (partial-index ON CONFLICT)" 204
dbcheck "T-Z2 granted twice is one row, first granted_by kept" \
  "SELECT count(*) || ':' || max(granted_by::text) FROM resource_grants WHERE user_id = '$U_M' AND agent_id = '$HR'" \
  "1:$U_A"
grant dashboard "$B_DASH" "$U_M"
expect "T-Z2 a grant naming company B's dashboard is not found" 404 '.error == "not found"'
grant agent "$HR" "$U_B"
expect "T-Z2 a grant to company B's user is not found" 404 '.error == "not found"'
dbcheck "T-Z2 …and neither wrote a row" \
  "SELECT count(*) FROM resource_grants WHERE dashboard_id = '$B_DASH' OR user_id = '$U_B'" "0"
mode dashboard "$B_DASH" restricted
expect "T-Z2 SetAccessMode on company B's dashboard is not found" 404
dbcheck "T-Z2 …and changes nothing" \
  "SELECT access_mode FROM dashboards WHERE id = '$B_DASH'" "open"
call GET "/api/access/dashboard/$SALES" "$TA"
expect "T-Z2 View of a resource nobody holds: its mode and []" 200 '.access_mode == "open" and .grants == []'
call GET "/api/access/dashboard/$B_DASH" "$TA"
expect "T-Z2 View of company B's resource is not found" 404
revoke agent "$HR" "$U_M"
expect "T-Z2 revoke HR from M" 204

# ===========================================================================
# T-Z4 / T-Z10 — HR restricted, nobody granted.
# ===========================================================================
mode agent "$HR" restricted
expect "T-Z4 restrict HR" 200 '.access_mode == "restricted"'

call GET /api/agents "$TM"
expect "T-Z4 M's roster omits HR and reachable_agent_ids matches" 200 \
  '([.agents[].id] | index("'"$HR"'")) == null and (.reachable_agent_ids | index("'"$HR"'")) == null'
call GET /api/agents "$TA"
expect "T-Z4 A's roster lists HR, absent from reachable_agent_ids" 200 \
  '([.agents[].id] | index("'"$HR"'")) != null and (.reachable_agent_ids | index("'"$HR"'")) == null'
call GET "/api/agents/$HR" "$TM"
expect "T-Z4 GET /api/agents/<HR> as M is not found" 404
for who in M A; do
  tok=$TM; [ "$who" = A ] && tok=$TA
  call POST /api/chat "$tok" '{"message":"hello HR","agent_id":"'"$HR"'"}'
  expect "T-Z4 $who: POST /api/chat picking HR → 404 no such agent" 404 '.error == "no such agent"'
  call POST /api/threads/$T_FIN/participants "$tok" '{"agent_id":"'"$HR"'"}'
  expect "T-Z4 $who: adding HR to a room → 404" 404
done

# A new chat with no agent named, HR the default: pinned to the next agent.
threads_before=$(sql "SELECT count(*) FROM conversation_threads WHERE company_id = '$CO_A'")
call POST /api/chat "$TM" '{"message":"what can you do?"}'
expect "T-Z4 M: a new chat with HR the default is accepted" 202 '.thread_id != null'
NEW_THREAD=$(printf '%s' "$BODY" | jq -r '.thread_id // empty')
dbcheck "T-Z4 …pinned to the next agent in the roster (Finance), not HR" \
  "SELECT agent_id FROM conversation_threads WHERE id = '${NEW_THREAD:-$NOBODY}'" "$FIN"

# T-Z10: the five shapes, as M and as A, neither granted.
for who in M A; do
  tok=$TM; [ "$who" = A ] && tok=$TA
  call GET /api/threads "$tok"
  expect "T-Z10 $who: the list omits the four HR shapes and keeps the retired-agent and Finance ones" 200 \
    '[.threads[].id] as $ids
     | ($ids | index("'"$T_HR"'")) == null and ($ids | index("'"$T_ROOM"'")) == null
       and ($ids | index("'"$T_REMOVED"'")) == null and ($ids | index("'"$T_UNPINNED"'")) == null
       and ($ids | index("'"$T_RETIRED"'")) != null and ($ids | index("'"$T_FIN"'")) != null'
  call GET /api/usage/threads "$tok"
  expect "T-Z10 $who: the usage list omits them too" 200 \
    '[(.threads // .rows // .)[] | (.thread_id // .id)] as $ids | ($ids | index("'"$T_HR"'")) == null and ($ids | index("'"$T_REMOVED"'")) == null'
done
call GET "/api/threads/$NOBODY" "$TM"; unknown_thread=$BODY
for t in "$T_HR" "$T_ROOM" "$T_REMOVED" "$T_UNPINNED"; do
  for route in "" /messages /participants; do
    call GET "/api/threads/$t$route" "$TM"
    expect "T-Z10 M: GET /api/threads/<${t: -2}>$route → 404" 404
  done
  call GET "/api/threads/$t" "$TA"
  expect "T-Z10 A: GET /api/threads/<${t: -2}> → 404" 404
  same "T-Z10 …byte-identical to an unknown id's" "$BODY" "$unknown_thread"
  call GET "/api/threads/$t/stream" "$TM"
  expect "T-Z10 M: the stream of <${t: -2}> → 404 before any upgrade" 404 '.error == "thread not found"'
  call GET "/api/usage/threads/$t" "$TM"
  expect "T-Z10 M: GET /api/usage/threads/<${t: -2}> → 404" 404
  call GET "/api/usage/threads/$t/events" "$TM"
  expect "T-Z10 M: …/events → 404" 404
  call POST "/api/threads/$t/participants" "$TM" '{"agent_id":"'"$OPS"'"}'
  expect "T-Z10 M: adding to the room of <${t: -2}> → 404" 404
done
call DELETE "/api/threads/$T_ROOM" "$TM"
expect "T-Z10 M: DELETE a hidden conversation → 404" 404
dbcheck "T-Z10 …and destroys nothing" "SELECT count(*) FROM conversation_threads WHERE id = '$T_ROOM'" "1"
msgs_before=$(sql "SELECT count(*) FROM messages WHERE thread_id = '$T_REMOVED'")
call POST /api/chat "$TM" '{"message":"what did HR say above?","thread_id":"'"$T_REMOVED"'"}'
expect "T-Z10 M: POST /api/chat into the removed-but-answered one → 404 no such conversation" 404 \
  '.error == "no such conversation"'
dbcheck "T-Z10 …nothing written" "SELECT count(*) FROM messages WHERE thread_id = '$T_REMOVED'" "$msgs_before"
call POST /api/chat "$TM" '{"message":"follow up","thread_id":"'"$T_HR"'"}'
expect "T-Z4 M: the next message on a conversation opened on HR → 404 (T-Z10 hides it; T-Z4's 403 predates T-Z10)" 404

# Grant M: everything back on the next request, no re-login.
grant agent "$HR" "$U_M"
expect "T-Z4 grant HR to M" 204
call GET /api/threads "$TM"
expect "T-Z10 granted M lists all six again" 200 '(.threads | map(select(.id | startswith("a0000000-0008"))) | length) == 6'
call GET "/api/threads/$T_REMOVED/messages" "$TM"
expect "T-Z10 granted M reads the removed-but-answered transcript" 200
call POST /api/chat "$TM" '{"message":"hello HR","agent_id":"'"$HR"'"}'
expect "T-Z4 granted M talks to HR on the next request" 202
call GET /api/threads "$TA"
expect "T-Z10 A, still not granted, still does not list them" 200 '([.threads[].id] | index("'"$T_HR"'")) == null'
revoke agent "$HR" "$U_M"
expect "T-Z4 revoke HR from M" 204
call POST /api/chat "$TM" '{"message":"hello again","agent_id":"'"$HR"'"}'
expect "T-Z4 revoked M is refused on the very next message" 404 '.error == "no such agent"'

# T-Z4's two hole arms are closed or moved: the transcript hole by T-Z10 (just
# above), the key hole by T-Z8 (below).

# ===========================================================================
# T-Z11 — generated documents, HR restricted.
# ===========================================================================
call GET "/api/documents/$NOBODY/pages/1" "$TM"; unknown_page=$BODY
call GET "/api/documents/$NOBODY/carousel" "$TM"; unknown_carousel=$BODY
call GET "/api/documents/$DOC_FIN/shares" "$TA"; unshared_list=$BODY
for who in M A; do
  tok=$TM; [ "$who" = A ] && tok=$TA
  call GET /api/documents "$tok"
  expect "T-Z11 $who: the list omits HR's two documents and keeps the other two" 200 \
    '[.documents[].id] as $ids | ($ids | index("'"$DOC_HR"'")) == null and ($ids | index("'"$DOC_HR_SLIDES"'")) == null and ($ids | index("'"$DOC_FIN"'")) != null and ($ids | index("'"$DOC_RENDER"'")) != null'
  call GET "/api/documents/$DOC_HR_SLIDES/pages/1" "$tok"
  expect "T-Z11 $who: HR's pages/1 → 404 document not found" 404 '.error == "document not found"'
  same "T-Z11 $who: …byte-identical to a random uuid's" "$BODY" "$unknown_page"
  call GET "/api/documents/$DOC_HR_SLIDES/carousel" "$tok"
  expect "T-Z11 $who: HR's carousel → 404" 404
  same "T-Z11 $who: …byte-identical to a random uuid's" "$BODY" "$unknown_carousel"
done
call GET "/api/documents/$DOC_HR/shares" "$TA"
expect "T-Z11 A: HR's share list → {\"shares\":[]} although two live links exist" 200 '.shares == []'
same "T-Z11 A: …identical to an unshared document's" "$BODY" "$unshared_list"
call POST "/api/documents/$DOC_HR/shares" "$TA" '{}'
expect "T-Z11 A: minting on HR's document → 404" 404
call DELETE "/api/documents/$DOC_HR/shares/$LINK_HR2" "$TA"
expect "T-Z11 A: revoking a link on HR's document is not refused" 204
dbcheck "T-Z11 …the second link is revoked, T-Z13's is not" \
  "SELECT count(*) FILTER (WHERE revoked_at IS NOT NULL) || ':' || count(*) FILTER (WHERE id = '$LINK_HR' AND revoked_at IS NULL) FROM report_shares WHERE document_id = '$DOC_HR'" \
  "1:1"

# ===========================================================================
# T-Z13 — the link to HR's document, held shut and opened again.
# ===========================================================================
anon /share/riz25-not-a-token; unknown_link=$BODY
views_before=$(sql "SELECT view_count FROM report_shares WHERE id = '$LINK_HR'")
view_rows_before=$(sql "SELECT count(*) FROM agent_actions WHERE company_id = '$CO_A' AND tool_name = 'report_share.view'")
anon /share/riz25-hr-doc-link
expect "T-Z13 HR restricted: /share/<token> → 404" 404
same "T-Z13 …byte-identical to a random token's" "$BODY" "$unknown_link"
dbcheck "T-Z13 …view_count unchanged" "SELECT view_count FROM report_shares WHERE id = '$LINK_HR'" "$views_before"
dbcheck "T-Z13 …no report_share.view row" \
  "SELECT count(*) FROM agent_actions WHERE company_id = '$CO_A' AND tool_name = 'report_share.view'" "$view_rows_before"
if [ -n "$HR_DECK_URL" ]; then
  presign_code=$(curl -s -o /dev/null -w '%{http_code}' "$HR_DECK_URL")
  case "$HR_DECK_URL" in *X-Amz-Expires=3600*) ttl=3600 ;; *) ttl=other ;; esac
  if [ "$presign_code" = 200 ]; then pass "§19e (documented gap) a download URL minted before HR was restricted still downloads (TTL $ttl s)"
  else fail "§19e (documented gap) a download URL minted before HR was restricted still downloads" "$presign_code — the gap may have closed; update access-grants §19e"; fi
else
  skip "§19e (documented gap) presigned URL" "no download_url (is MINIO_ENDPOINT set?)"
fi
grant agent "$HR" "$U_A"
expect "T-Z13 grant HR to A (the list and the mint are A's, granted)" 204
call GET "/api/documents/$DOC_HR/shares" "$TA"
expect "T-Z13 A granted: the link lists live: true, paused: true" 200 \
  '[.shares[] | select(.id == "'"$LINK_HR"'") | (.live and .paused)] == [true]'
call POST "/api/documents/$DOC_HR/shares" "$TA" '{}'
expect "T-Z13 A granted: minting on it → 409 naming the restricted agent" 409
call GET "/api/documents/$DOC_HR_SLIDES/pages/1" "$TA"
expect "T-Z11 granted A: HR's pages are back on the next request" 200
call GET "/api/documents/$DOC_HR_SLIDES/carousel" "$TA"
expect "T-Z11 granted A: …and its carousel" 200
call GET /api/documents "$TA"
expect "T-Z11 granted A: HR's document is listed again" 200 '[.documents[].id] | index("'"$DOC_HR"'") != null'
revoke agent "$HR" "$U_A"
expect "T-Z13 revoke HR from A" 204

# ===========================================================================
# T-Z14 — proposals from HR's conversation.
# ===========================================================================
refused_before=$(refused_rows)
call GET "/api/actions/$NOBODY" "$TM"; unknown_proposal=$BODY
for who in M A; do
  tok=$TM; [ "$who" = A ] && tok=$TA
  call GET /api/actions/pending "$tok"
  expect "T-Z14 $who: pending omits HR's proposal and keeps the one with no conversation" 200 \
    '[(.actions // .invocations // .pending)[].id] == ["'"$PROP_NONE"'"]'
done
call GET "/api/actions/$PROP_HR" "$TM"
expect "T-Z14 M: GET HR's proposal → 404 no such action proposal" 404 '.error == "no such action proposal"'
same "T-Z14 …byte-identical to a random uuid's" "$BODY" "$unknown_proposal"
call POST "/api/actions/$PROP_HR/approve" "$TM"
expect "T-Z14 M: approve → 404" 404 '.error == "no such action proposal"'
call POST "/api/actions/$PROP_HR/reject" "$TA"
expect "T-Z14 A: reject → 404" 404 '.error == "no such action proposal"'
dbcheck "T-Z14 …the row is still proposed" "SELECT status FROM action_invocations WHERE id = '$PROP_HR'" "proposed"
call GET "/api/audit/actions?limit=200" "$TA"
expect "§20d (documented gap) the admin's ledger lists the propose_action call from HR's hidden conversation" 200 \
  '[(.actions // .rows // .)[] | select(.tool_name == "propose_action" and .thread_id == "'"$T_HR"'")] | length == 1'
dbcheck "T-Z14 …three access.refused rows of kind conversation" \
  "SELECT count(*) - $refused_before FROM agent_actions WHERE company_id = '$CO_A' AND tool_name = 'access.refused'" "3"

# The waiting case: an admin-only kind, HR granted to M alone.
call PUT /api/actions/config/send_message "$TA" '{"enabled":true,"requires_approval":true,"allowed_roles":["admin"]}'
expect "T-Z14 waiting: send_message decided by admins only" 200
grant agent "$HR" "$U_M"
expect "T-Z14 waiting: HR granted to M alone" 204
call GET /api/actions/pending "$TM"
expect "T-Z14 waiting: M sees HR's proposal, and may not decide it" 200 \
  '[(.actions // .invocations // .pending)[] | select(.id == "'"$PROP_HR"'") | (.can_decide // false)] == [false]'
call POST "/api/actions/$PROP_HR/approve" "$TM"
expect "T-Z14 waiting: M's approve is refused by role" 403
call GET /api/actions/pending "$TA"
expect "T-Z14 waiting: A, not granted, does not see it" 200 \
  '[(.actions // .invocations // .pending)[].id] | index("'"$PROP_HR"'") == null'
dbcheck "T-Z14 waiting: …so it waits, still proposed" "SELECT status FROM action_invocations WHERE id = '$PROP_HR'" "proposed"
call PUT /api/actions/config/send_message "$TA" '{"enabled":true,"requires_approval":true,"allowed_roles":["admin","member"]}'
expect "T-Z14 members may decide again" 200
call POST "/api/actions/$PROP_HR/approve" "$TM"
expect "T-Z14 granted M approves it on the next request" 200
revoke agent "$HR" "$U_M"
expect "T-Z14 revoke HR from M" 204

# ===========================================================================
# T-Z8 part 2 — the doors without a person: an API key, the widget, a binding.
# ===========================================================================
call POST /api/api-keys "$TA" '{"name":"riz25 finance only","scopes":["write:reports","read:documents","write:chat","read:threads"],"agent_ids":["'"$FIN"'"]}'
expect "T-Z8 A mints a key listing Finance only" 201 '.token != null or .key != null'
KEY=$(printf '%s' "$BODY" | jq -r '.token // .key // empty')
KEY_ID=$(printf '%s' "$BODY" | jq -r '.key.id // empty')
call POST /api/api-keys "$TA" '{"name":"riz25 no list","scopes":["read:documents"]}'
expect "T-Z8 A mints a key naming no agents" 201
OPEN_KEY_ID=$(printf '%s' "$BODY" | jq -r '.key.id // empty')
dbcheck "T-Z8 …a key made with no list stores '{}', never NULL" \
  "SELECT agent_ids::text FROM api_keys WHERE id = '${OPEN_KEY_ID:-$NOBODY}'" "{}"

v1() {
  local tmp; tmp=$(mktemp)
  # Every /v1 write needs an Idempotency-Key (T-A1); one per call, since each
  # arm is a different logical request.
  CODE=$(curl -s -o "$tmp" -w '%{http_code}' -X "$1" -H "Authorization: Bearer $KEY" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: riz25-$(date +%s)-$RANDOM" \
    ${3:+-d "$3"} "$API$2")
  BODY=$(cat "$tmp"); rm -f "$tmp"
}
v1 GET /v1/agents
expect "T-Z8 GET /v1/agents lists Finance alone" 200 '[(.data // .agents)[].id] == ["'"$FIN"'"]'
v1 POST /v1/reports '{"prompt":"Summarise headcount","format":"pdf","user_ref":"riz25-integrator","agent_id":"'"$OPS"'"}'
expect "T-Z8 /v1/reports naming Ops → 404 agent_not_found" 404 '(.error.code // .code // .error) == "agent_not_found"'
key_threads_before=$(sql "SELECT count(*) FROM conversation_threads WHERE company_id = '$CO_A'")
v1 POST /v1/reports '{"prompt":"Summarise headcount","format":"pdf","user_ref":"riz25-integrator"}'
expect "T-Z8 /v1/reports naming none, HR the default → 403 agent_not_allowed" 403 '(.error.code // .code // .error) == "agent_not_allowed"'
dbcheck "T-Z8 …and no new conversation_threads row" \
  "SELECT count(*) FROM conversation_threads WHERE company_id = '$CO_A'" "$key_threads_before"
v1 POST /v1/reports '{"prompt":"Summarise salary by department","format":"pdf","user_ref":"riz25-integrator","agent_id":"'"$FIN"'"}'
expect "T-Z8/T-Z6 /v1/reports naming Finance (scoped to HR warehouse) → 202" 202
FIN_THREAD=$(printf '%s' "$BODY" | jq -r '.thread_id // empty')
if [ "${NO_DB:-}" != 1 ]; then
  sql "UPDATE conversation_threads SET agent_id = '$OPS' WHERE id = '${FIN_THREAD:-$NOBODY}'" >/dev/null
  v1 POST /v1/reports '{"prompt":"again","format":"pdf","thread_id":"'"${FIN_THREAD:-$NOBODY}"'"}'
  expect "T-Z8 /v1/reports on a thread running as Ops → 403" 403
else
  skip "T-Z8 /v1/reports on a thread running as Ops → 403" "NO_DB=1 (the arm re-pins a thread)"
fi

# The widget: a key for a made-up origin, a signed session, HR restricted.
ORIGIN=http://localhost:5599
call POST /api/embed-keys "$TA" '{"name":"riz25 widget","allowed_origins":["'"$ORIGIN"'"]}'
expect "T-Z8 A mints an embed key" 201
CLIENT_KEY=$(printf '%s' "$BODY" | jq -r '.client_key // .key.client_key // .embed_key.client_key // empty')
SECRET=$(printf '%s' "$BODY" | jq -r '.secret // .key.secret // empty')
EXP=$(( $(date +%s) + 600 ))
SIG=$(printf '%s' "riz25-visitor:$EXP" | openssl dgst -sha256 -hmac "$SECRET" -hex)
SIG=${SIG##* }
tmp=$(mktemp)
CODE=$(curl -s -o "$tmp" -w '%{http_code}' -X POST -H "Origin: $ORIGIN" -H 'Content-Type: application/json' \
  -d '{"client_key":"'"$CLIENT_KEY"'","user_ref":"riz25-visitor","exp":'"$EXP"',"signature":"'"$SIG"'"}' "$API/api/embed/session")
BODY=$(cat "$tmp"); rm -f "$tmp"
expect "T-Z8 the widget session mints" 200 '.token != null'
ET=$(printf '%s' "$BODY" | jq -r '.token // empty')
EXTRA_HEADER="Origin: $ORIGIN"
call GET /api/embed/config "$ET"
expect "T-Z8 widget: /api/embed/config omits HR" 200 '[.agents[]?.id] | index("'"$HR"'") == null'
not_cleared_before=0
[ -n "${METRICS_TOKEN:-}" ] && not_cleared_before=$(metric 'argentum_access_refusals_total{kind="agent",reason="not_cleared"}')
embed_rows_before=$(sql "SELECT count(*) FROM agent_actions WHERE company_id = '$CO_A' AND tool_name = 'access.refused' AND actor_kind = 'embed'")
call POST /api/embed/chat "$ET" '{"message":"hello","agent_id":"'"$HR"'"}'
expect "T-Z8 widget: a send picking HR → 404" 404
if [ -n "${METRICS_TOKEN:-}" ]; then
  now=$(metric 'argentum_access_refusals_total{kind="agent",reason="not_cleared"}')
  if [ "$((now - not_cleared_before))" = 1 ]; then pass "T-Z9(3) {kind=\"agent\",reason=\"not_cleared\"} moved by 1"
  else fail "T-Z9(3) {kind=\"agent\",reason=\"not_cleared\"} moved by 1" "moved by $((now - not_cleared_before))"; fi
else
  skip "T-Z9(3) {kind=\"agent\",reason=\"not_cleared\"} moved by 1" "METRICS_TOKEN unset"
fi
dbcheck "T-Z9(3) …and one access.refused row whose actor is embed and the visitor's ref" \
  "SELECT count(*) - $embed_rows_before || ':' || coalesce(max(actor_ref) FILTER (WHERE actor_kind = 'embed'), '') FROM agent_actions WHERE company_id = '$CO_A' AND tool_name = 'access.refused' AND actor_kind = 'embed'" \
  "1:riz25-visitor"
call POST /api/embed/chat "$ET" '{"message":"hello"}'
expect "T-Z8 widget: HR the default, a new conversation is accepted" 202
EMBED_THREAD=$(printf '%s' "$BODY" | jq -r '.thread_id // empty')
dbcheck "T-Z8 widget: …pinned to the first open agent (Finance)" \
  "SELECT agent_id FROM conversation_threads WHERE id = '${EMBED_THREAD:-$NOBODY}'" "$FIN"
EXTRA_HEADER=

# A channel binding of restricted HR, with and without the acknowledgement.
call POST /api/agent-bindings "$TA" '{"agent_id":"'"$HR"'","channel":"discord","external_id":"riz25-hr-room-2"}'
expect "T-Z8 binding restricted HR without the acknowledgement → 400" 400
call POST /api/agent-bindings "$TA" '{"agent_id":"'"$HR"'","channel":"discord","external_id":"riz25-hr-room-2","acknowledge_restricted":true}'
expect "T-Z8 …with it → 201" 201
call GET "/api/audit/actions?limit=50" "$TA"
expect "T-Z8 the audit log names A on agent_binding.acknowledge_restricted" 200 \
  '[(.actions // .rows // .)[] | select(.tool_name == "agent_binding.acknowledge_restricted") | .actor_ref] | index("'"$U_A"'") != null'
call GET /api/agent-bindings "$TA"
expect "T-Z8 the binding made before the restriction reads as silenced (no acknowledgement)" 200 \
  '[.bindings[] | select(.id == "'"$BINDING_HR"'") | (.restricted_ack_at == null)] == [true]'
call PUT "/api/agent-bindings/$BINDING_HR/acknowledgement" "$TA"
expect "T-Z8 A acknowledges it" 200
TA2=${ADMIN2_TOKEN:-}
if [ -n "$TA2" ]; then
  call PUT "/api/agent-bindings/$BINDING_HR/acknowledgement" "$TA2"
  expect "T-Z8 a second admin acknowledges again" 200
  dbcheck "T-Z8 Acknowledge twice keeps the first admin" \
    "SELECT restricted_ack_by FROM agent_channel_bindings WHERE id = '$BINDING_HR'" "$U_A"
else
  skip "T-Z8 Acknowledge twice keeps the first admin" "ADMIN2_TOKEN unset"
fi

mode agent "$HR" open
expect "T-Z4 re-open HR" 200

# T-Z13 part 2: HR open again, the same token plays and is counted.
anon /share/riz25-hr-doc-link
expect "T-Z13 HR re-opened: the same token plays" 200 '.plan != null'
dbcheck "T-Z13 …its view counted, revoked_at never set" \
  "SELECT (view_count = $((${views_before:-0} + 1)))::text || ':' || (revoked_at IS NULL)::text FROM report_shares WHERE id = '$LINK_HR'" \
  "true:true"
call GET "/api/documents/$DOC_HR/shares" "$TA"
expect "T-Z13 …and the list shows no pause" 200 '[.shares[] | select(.id == "'"$LINK_HR"'") | (.paused // false)] == [false]'

# ===========================================================================
# T-Z5 / T-Z3 — Payroll restricted with four links.
# ===========================================================================
call GET "/api/dashboards/$NOBODY" "$TM"; unknown_dash=$BODY; unknown_dash_code=$CODE
mode dashboard "$PAYROLL" restricted
expect "T-Z5 restrict Payroll → 200 {\"revoked_shares\":2}" 200 '.revoked_shares == 2'
dbcheck "T-Z5 …exactly the two live links revoked; the revoked and expired ones untouched" \
  "SELECT string_agg(id::text || '=' || CASE WHEN id IN ('$SHARE_LIVE1','$SHARE_LIVE2') THEN (revoked_at > now() - interval '5 minutes')::text WHEN id = '$SHARE_REVOKED' THEN (revoked_at < now() - interval '1 hour')::text ELSE (revoked_at IS NULL)::text END, ',' ORDER BY id) FROM dashboard_shares WHERE dashboard_id = '$PAYROLL'" \
  "$SHARE_LIVE1=true,$SHARE_LIVE2=true,$SHARE_REVOKED=true,$SHARE_EXPIRED=true"
anon /share/dashboard/riz25-payroll-live-1
expect "T-Z5 the old token → 404 This link is not available." 404 '.error == "This link is not available."'
for who in M A; do
  tok=$TM; [ "$who" = A ] && tok=$TA
  call GET /api/dashboards "$tok"
  expect "T-Z5 $who: the list omits Payroll" 200 '[.dashboards[].id] | index("'"$PAYROLL"'") == null'
  call GET "/api/dashboards/$PAYROLL" "$tok"
  expect "T-Z3/T-Z5 $who: GET /api/dashboards/<payroll> → 403 naming the kind" 403 '.resource_kind == "dashboard"'
  call GET "/api/dashboards/$PAYROLL/data" "$tok"
  expect "T-Z3/T-Z5 $who: …/data → 403 naming the kind" 403 '.resource_kind == "dashboard"'
done
call GET "/api/dashboards/$PAYROLL/shares" "$TA"
expect "T-Z5 A: …/shares → 403 naming the kind" 403 '.resource_kind == "dashboard"'
call POST "/api/dashboards/$PAYROLL/shares" "$TA" '{}'
expect "T-Z5 A: POST …/shares → 409 with the reason" 409
grant dashboard "$PAYROLL" "$U_A"
expect "T-Z5 grant Payroll to A" 204
call POST "/api/dashboards/$PAYROLL/shares" "$TA" '{}'
expect "T-Z5 A granted: POST …/shares → still 409" 409
call GET "/api/dashboards/$PAYROLL/shares" "$TA"
expect "T-Z5 A granted: the share list opens" 200
revoke dashboard "$PAYROLL" "$U_A"
expect "T-Z5 revoke Payroll from A" 204
call DELETE "/api/dashboards/$PAYROLL/shares/$SHARE_REVOKED" "$TA"
expect "T-Z5 A, not granted: revoking a link is not refused" 204
grant dashboard "$PAYROLL" "$U_M"
expect "T-Z5 grant Payroll to M" 204
call GET "/api/dashboards/$PAYROLL" "$TM"
expect "T-Z3 granted M opens it on the next request" 200
call GET "/api/dashboards/$PAYROLL/data" "$TM"
expect "T-Z3 …and runs it" 200
revoke dashboard "$PAYROLL" "$U_M"
expect "T-Z5 revoke Payroll from M" 204
call GET "/api/dashboards/$PAYROLL" "$TM"
expect "T-Z3 revoked M is refused on the very next request" 403 '.resource_kind == "dashboard"'
call GET "/api/dashboards/$B_DASH" "$TM"
expect "T-Z3 company B's real dashboard id → the handler's own 404" "$unknown_dash_code"
same "T-Z3 …byte-identical to an unknown id's" "$BODY" "$unknown_dash"
mode dashboard "$SCRATCH" restricted
expect "T-Z5 restrict Scratch board, nobody granted" 200
call DELETE "/api/dashboards/$SCRATCH" "$TA"
expect "T-Z5 A, not granted: deleting a restricted dashboard is not refused" 204
mode dashboard "$PAYROLL" open
expect "T-Z5 re-open Payroll" 200

# ===========================================================================
# T-Z6 part 1 — a restricted source and a restricted document.
# ===========================================================================
mode connection "$HR_SRC" restricted
expect "T-Z6 restrict HR warehouse, nobody granted" 200
call GET /api/connections "$TM"
expect "T-Z6 M's source list omits it" 200 '[(.connections // .)[].id] | index("'"$HR_SRC"'") == null'
call GET /api/connections "$TA"
expect "T-Z6 A's still lists it" 200 '[(.connections // .)[].id] | index("'"$HR_SRC"'") != null'
call POST "/api/connections/$HR_SRC/test-rag" "$TA" '{"query":"salary"}'
expect "T-Z6 A: …/test-rag → 403 {\"resource_kind\":\"connection\"}" 403 '.resource_kind == "connection"'
call POST "/api/connections/$HR_SRC/regenerate-description" "$TA"
expect "T-Z6 A: …/regenerate-description → 403" 403 '.resource_kind == "connection"'
call POST "/api/connections/$HR_SRC/freshness/test" "$TA" '{"sql":"SELECT now()"}'
expect "T-Z6 A: …/freshness/test → 403" 403 '.resource_kind == "connection"'
call POST "/api/connections/$HR_SRC/test" "$TA"
if [ "$CODE" != 403 ]; then pass "T-Z6 A: …/test is not refused ($CODE)"; else fail "T-Z6 A: …/test is not refused" "$CODE $BODY"; fi
call GET /api/metrics "$TM"
expect "§17c M still lists the restricted source's metric, sql_template and all (documented gap)" 200 \
  '[.metrics[] | select(.source_id == "'"$HR_SRC"'") | .sql_template] | length == 1'

mode document "$PAYROLL_PDF" restricted
expect "T-Z6 restrict Payroll 2026.pdf, nobody granted" 200
for who in M A; do
  tok=$TM; [ "$who" = A ] && tok=$TA
  call GET /api/knowledge/documents "$tok"
  expect "T-Z6 $who: Knowledge omits it" 200 '[.documents[].id] | index("'"$PAYROLL_PDF"'") == null'
  for route in "" /tables /pages/1; do
    call GET "/api/knowledge/documents/$PAYROLL_PDF$route" "$tok"
    expect "T-Z6 $who: GET …/<payroll pdf>$route → 403 {\"resource_kind\":\"document\"}" 403 '.resource_kind == "document"'
  done
  call GET "/api/knowledge/tables/$PAYROLL_TABLE" "$tok"
  expect "T-Z6 $who: its table by its own id → 403" 403 '.resource_kind == "document"'
done
call PATCH "/api/knowledge/tables/$PAYROLL_TABLE" "$TA" '{"columns":"not a list"}'
expect "T-Z6 A: PATCH with a bad body → 403, not 400" 403 '.resource_kind == "document"'
call POST "/api/knowledge/tables/$PAYROLL_TABLE/unpublish" "$TA"
if [ "$CODE" != 403 ]; then pass "T-Z6 A: …/unpublish is not refused ($CODE)"; else fail "T-Z6 A: …/unpublish is not refused" "$CODE $BODY"; fi
grant document "$PAYROLL_PDF" "$U_M"
expect "T-Z6 grant the document to M" 204
grant connection "$HR_SRC" "$U_M"
expect "T-Z6 grant the source to M" 204
call GET /api/knowledge/documents "$TM"
expect "T-Z6 granted M: Knowledge lists it on the next request" 200 '[.documents[].id] | index("'"$PAYROLL_PDF"'") != null'
call GET "/api/knowledge/documents/$PAYROLL_PDF/tables" "$TM"
# The seeded document has no page artifacts, so the route's own answer is a 409
# "not parsed yet" — what matters here is that it is no longer the 403.
if [ "$CODE" != 403 ]; then pass "T-Z6 granted M: …its tables route is no longer refused ($CODE)"
else fail "T-Z6 granted M: …its tables route is no longer refused" "$CODE $BODY"; fi
call GET /api/connections "$TM"
expect "T-Z6 granted M: the source is listed" 200 '[(.connections // .)[].id] | index("'"$HR_SRC"'") != null'
revoke document "$PAYROLL_PDF" "$U_M"
revoke connection "$HR_SRC" "$U_M"
mode document "$PAYROLL_PDF" open
expect "T-Z6 re-open the document" 200
mode connection "$HR_SRC" open
expect "T-Z6 re-open the source" 200

# ===========================================================================
# R9 / Q13 — every route with an id parameter, called with `x`.
#
# RIZ-25 wrote this section as the acceptance test for a follow-up: its run found
# a malformed id answering 500, 503 or a body quoting the driver on 44 routes
# across fourteen repositories (live-gate-backlog.md §7c, the T-Z1 row). RIZ-26
# is that follow-up — one check at the chain, RequireWellFormedIDs — so the rule
# here tightened from "no 5xx and no driver text" to the answer itself:
#
#   sweep         a path id: exactly `404 {"error":"not found"}`, the
#                 middleware's, before any access check, handler or query
#   sweep_body    an id in the body: byte-identical to a well-formed id that
#                 names nothing ($NOBODY), so a typo reads as a deleted row
#   sweep_keyless the share pages, whose `:token` is not an id: the old rule
#
# It also covers the `/api` id routes RIZ-25's sweep did not reach (RIZ-26 R9).
# ===========================================================================
sweep() {
  local method=$1 path=$2 token=$3 body=${4:-}
  call "$method" "$path" "$token" "$body"
  if [ "$CODE" = 404 ] && [ "$BODY" = '{"error":"not found"}' ]; then
    pass "R9 $method $path → 404"
  else
    fail "R9 $method $path" "want 404 {\"error\":\"not found\"}, got $CODE ${BODY:0:160}"
  fi
}
# sweep_optional is sweep for a route that exists only where a provider is
# configured (voice, read-aloud). Absent, it is the router's own plain-text 404,
# which says nothing about ids and is reported as such rather than as a pass of
# the check.
sweep_optional() {
  call "$1" "$2" "$3" "${4:-}"
  if [ "$CODE" = 404 ] && [ "$BODY" = '404 page not found' ]; then
    skip "R9 $1 $2" "not registered on this deployment"
    return
  fi
  sweep "$@"
}
sweep_keyless() {
  local path=$1
  anon "$path"
  case "$CODE:$BODY" in
    5??:*) fail "R9 GET $path" "$CODE ${BODY:0:160}" ;;
    *pq:* | *22P02*) fail "R9 GET $path" "$CODE body names the driver: ${BODY:0:160}" ;;
    *) pass "R9 GET $path → $CODE" ;;
  esac
}
# sweep_body ARM METHOD PATH TOKEN TEMPLATE — TEMPLATE with @ID@ as `x`, then as
# $NOBODY; status and body must match, and neither may name the driver. The `x`
# call goes first so a write the unknown id is allowed to make (a room's thread
# is created before its participants are refused) cannot hide one `x` made.
sweep_body() {
  local arm=$1 method=$2 path=$3 token=$4 template=$5 bad_code bad_body
  call "$method" "$path" "$token" "${template//@ID@/x}"
  bad_code=$CODE bad_body=$BODY
  call "$method" "$path" "$token" "${template//@ID@/$NOBODY}"
  case "$bad_body" in
    *pq:* | *22P02*) fail "R9 $arm" "body names the driver: ${bad_body:0:160}"; return ;;
  esac
  if [ "$bad_code" = "$CODE" ] && [ "$bad_body" = "$BODY" ]; then
    pass "R9 $arm → $CODE, as an unknown id"
  else
    fail "R9 $arm" "x: $bad_code ${bad_body:0:100} | unknown: $CODE ${BODY:0:100}"
  fi
}

for kind in agent dashboard connection document; do
  sweep GET "/api/access/$kind/x" "$TA"
  sweep PUT "/api/access/$kind/x/mode" "$TA" '{"access_mode":"restricted"}'
  sweep PUT "/api/access/$kind/x/grants/$U_M" "$TA"
  sweep DELETE "/api/access/$kind/x/grants/$U_M" "$TA"
done
sweep PUT "/api/access/agent/$HR/grants/x" "$TA"
sweep DELETE "/api/access/agent/$HR/grants/x" "$TA"
sweep GET /api/users/x/grants "$TA"
sweep GET /api/users/x/capabilities "$TA"
sweep PUT /api/users/x/capabilities/voice "$TA"
sweep DELETE /api/users/x/capabilities/voice "$TA"
sweep PATCH /api/users/x "$TA" '{"role":"member"}'
sweep DELETE /api/users/x "$TA"
sweep GET /api/agents/x "$TM"
sweep PUT /api/agents/x "$TA" '{"name":"x"}'
sweep DELETE /api/agents/x "$TA"
sweep PUT /api/agents/x/default "$TA"
sweep GET /api/agents/x/skills "$TA"
sweep PUT /api/agents/x/skills "$TA" '{"skill_ids":[]}'
sweep GET /api/dashboards/x "$TM"
sweep GET /api/dashboards/x/data "$TM"
sweep DELETE /api/dashboards/x "$TA"
sweep GET /api/dashboards/x/shares "$TA"
sweep POST /api/dashboards/x/shares "$TA" '{}'
sweep DELETE "/api/dashboards/$SALES/shares/x" "$TA"
sweep_keyless /share/dashboard/x
sweep_keyless /share/x
sweep GET /api/threads/x "$TM"
sweep DELETE /api/threads/x "$TM"
sweep GET /api/threads/x/messages "$TM"
sweep GET /api/threads/x/participants "$TM"
sweep POST /api/threads/x/participants "$TM" '{"agent_id":"'"$FIN"'"}'
sweep DELETE "/api/threads/$T_FIN/participants/x" "$TM"
sweep GET /api/threads/x/stream "$TM"
sweep GET /api/usage/threads/x "$TM"
sweep GET /api/usage/threads/x/events "$TM"
sweep GET /api/documents/x/pages/1 "$TM"
sweep GET /api/documents/x/carousel "$TM"
sweep GET /api/documents/x/shares "$TA"
sweep POST /api/documents/x/shares "$TA" '{}'
sweep DELETE "/api/documents/$DOC_FIN/shares/x" "$TA"
sweep GET /api/knowledge/documents/x "$TM"
sweep GET /api/knowledge/documents/x/tables "$TM"
sweep GET /api/knowledge/documents/x/pages/1 "$TM"
sweep DELETE /api/knowledge/documents/x "$TA"
sweep GET /api/knowledge/tables/x "$TM"
sweep PATCH /api/knowledge/tables/x "$TA" '{"title":"y"}'
sweep POST /api/knowledge/tables/x/apply "$TA"
sweep POST /api/knowledge/tables/x/unpublish "$TA"
sweep PATCH /api/connections/x "$TA" '{"label":"y"}'
sweep PUT /api/connections/x/dsn "$TA" '{}'
sweep PUT /api/connections/x/allowlist "$TA" '{}'
sweep PUT /api/connections/x/freshness "$TA" '{}'
sweep POST /api/connections/x/freshness/test "$TA" '{"sql":"SELECT now()"}'
sweep POST /api/connections/x/default "$TA"
sweep POST /api/connections/x/regenerate-description "$TA"
sweep POST /api/connections/x/rescan "$TA"
sweep POST /api/connections/x/reindex-embeddings "$TA"
sweep POST /api/connections/x/test-rag "$TA" '{"query":"q"}'
sweep POST /api/connections/x/test "$TA"
sweep DELETE /api/connections/x "$TA"
sweep GET /api/actions/x "$TM"
sweep POST /api/actions/x/approve "$TM"
sweep POST /api/actions/x/reject "$TM"
sweep PUT /api/agent-bindings/x/acknowledgement "$TA"
sweep DELETE /api/agent-bindings/x "$TA"
sweep GET /api/scheduled-tasks/x "$TM"
sweep PATCH /api/scheduled-tasks/x "$TM" '{"enabled":true}'
sweep DELETE /api/scheduled-tasks/x "$TA"
sweep GET /api/scheduled-tasks/x/runs "$TM"
sweep GET "/api/scheduled-tasks/$NOBODY/runs/x" "$TM"
sweep GET /api/metrics/x "$TM"
sweep PUT /api/metrics/x "$TA" '{}'
sweep DELETE /api/metrics/x "$TA"
sweep DELETE /api/api-keys/x "$TA"

# RIZ-26's additions: the `/api` id routes outside roadmap 12.
sweep GET /api/watchers/x "$TA"
sweep PUT /api/watchers/x "$TA" '{}'
sweep DELETE /api/watchers/x "$TA"
sweep GET /api/watchers/x/events "$TA"
sweep POST /api/watchers/x/dry-run "$TA"
sweep GET /api/mcp-servers/x "$TA"
sweep PUT /api/mcp-servers/x "$TA" '{}'
sweep DELETE /api/mcp-servers/x "$TA"
sweep POST /api/mcp-servers/x/refresh "$TA"
sweep PUT "/api/mcp-servers/$NOBODY/tools/x" "$TA" '{"enabled":false}'
sweep PUT /api/webhooks/x "$TA" '{}'
sweep DELETE /api/webhooks/x "$TA"
sweep PUT /api/embed-keys/x "$TA" '{}'
sweep DELETE /api/embed-keys/x "$TA"
sweep GET /api/skills/x "$TA"
sweep PUT /api/skills/x "$TA" '{}'
sweep DELETE /api/skills/x "$TA"
sweep DELETE /api/http-endpoints/x "$TA"
sweep GET /api/post-images/x/content "$TA"
sweep DELETE /api/post-images/x "$TA"
sweep GET /api/messages/x/feedback "$TM"
sweep POST /api/messages/x/feedback "$TM" '{"rating":"up"}'
sweep POST /api/messages/x/suggestion-picked "$TM" '{}'
# The two voice routes ask for the `voice` capability one link ahead of the id,
# so A holds it for these two rows and gives it back after.
call PUT "/api/users/$U_A/capabilities/voice" "$TA"
sweep_optional POST /api/threads/x/voice "$TA"
sweep_optional GET /api/messages/x/audio "$TA"
call DELETE "/api/users/$U_A/capabilities/voice" "$TA"
# The widget's own group runs the same check after its session.
if [ -n "${ET:-}" ]; then
  EXTRA_HEADER="Origin: $ORIGIN"
  call GET /api/embed/threads/x/messages "$ET"
  if [ "$CODE" = 401 ]; then
    skip "R9 GET /api/embed/threads/x/messages" "the widget session minted above has expired"
  else
    sweep GET /api/embed/threads/x/messages "$ET"
  fi
  EXTRA_HEADER=
fi

# Ids in a body, where no path check can see them (RIZ-26 R5, R6). Each is the
# answer an unknown id gets, and leaves nothing behind.
msgs_before=$(sql "SELECT count(*) FROM messages m JOIN conversation_threads t ON t.id = m.thread_id WHERE t.company_id = '$CO_A'")
threads_before=$(sql "SELECT count(*) FROM conversation_threads WHERE company_id = '$CO_A'")
sweep_body "POST /api/chat thread_id" POST /api/chat "$TM" '{"message":"hi","thread_id":"@ID@"}'
sweep_body "POST /api/chat agent_id" POST /api/chat "$TM" '{"message":"hi","agent_id":"@ID@"}'
sweep_body "POST /api/threads/:id/participants agent_id" POST "/api/threads/$T_FIN/participants" "$TM" '{"agent_id":"@ID@"}'
sweep_body "POST /api/threads agent_id" POST /api/threads "$TM" '{"agent_id":"@ID@"}'
dbcheck "R9 …no message written by a malformed or unknown body id" \
  "SELECT count(*) FROM messages m JOIN conversation_threads t ON t.id = m.thread_id WHERE t.company_id = '$CO_A'" "$msgs_before"
dbcheck "R9 …and no thread" "SELECT count(*) FROM conversation_threads WHERE company_id = '$CO_A'" "$threads_before"
# A participant that is no agent is refused after its thread exists — today's
# behaviour for an unknown id — and before it for `x`, so `x` alone is counted.
call POST /api/threads "$TM" '{"participant_ids":["x"]}'
X_CODE=$CODE X_BODY=$BODY
dbcheck "R9 POST /api/threads participant_ids [x] writes no thread" \
  "SELECT count(*) FROM conversation_threads WHERE company_id = '$CO_A'" "$threads_before"
call POST /api/threads "$TM" '{"participant_ids":["'"$NOBODY"'"]}'
if [ "$X_CODE" = "$CODE" ] && [ "$X_BODY" = "$BODY" ]; then
  pass "R9 POST /api/threads participant_ids → $CODE, as an unknown id"
else
  fail "R9 POST /api/threads participant_ids" "x: $X_CODE ${X_BODY:0:100} | unknown: $CODE ${BODY:0:100}"
fi

# Leave no key behind that a later run would not expect.
[ -n "${KEY_ID:-}" ] && call DELETE "/api/api-keys/$KEY_ID" "$TA"
[ -n "${OPEN_KEY_ID:-}" ] && call DELETE "/api/api-keys/$OPEN_KEY_ID" "$TA"

echo "# $PASSES passed, $FAILS failed, $SKIPS skipped"
[ "$FAILS" = 0 ]
