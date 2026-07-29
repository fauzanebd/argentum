#!/usr/bin/env bash
#
# Runs every published sample against a live deployment.
#
#   ARGENTUM_BASE_URL=http://localhost:8080 ARGENTUM_API_KEY=arg_… ./run.sh deterministic
#   ARGENTUM_BASE_URL=…                     ARGENTUM_API_KEY=…     ./run.sh agentic
#
# **Split by cost, and that is the whole reason for the argument.** The
# deterministic samples cost a render, so CI runs them on every push. The
# agentic ones spend real tokens on the demo tenant, so putting them in the
# per-push path would bill an LLM turn for every commit in the monorepo — they
# run nightly instead, where a broken example is still caught within a day.
#
# It builds the SDKs into a scratch directory and installs them there, from a
# packed tarball and a source tree, rather than importing them from the
# workspace. That is deliberate: the quickstart tells a reader to `npm install`
# and `pip install` into an empty directory, and a runner that resolved the
# packages through a workspace symlink would prove that path works when it
# might not.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../../.." && pwd)
mode=${1:-deterministic}

: "${ARGENTUM_BASE_URL:?set ARGENTUM_BASE_URL, e.g. http://localhost:8080}"
: "${ARGENTUM_API_KEY:?set ARGENTUM_API_KEY to a key with write:reports, read:documents and write:chat}"
export ARGENTUM_BASE_URL ARGENTUM_API_KEY

work=${ARGENTUM_EXAMPLE_DIR:-$(mktemp -d)}
mkdir -p "$work"
cd "$work"
echo "workspace: $work"

started=$(date +%s)

step() { printf '\n=== %s\n' "$*"; }
fail() { printf '\nFAILED: %s\n' "$*" >&2; exit 1; }

# A PDF that is not a PDF is the failure this catches: a 30-byte file whose
# contents are an error envelope the example wrote to disk without looking.
assert_pdf() {
  local file=$1 min=${2:-2000}
  [ -f "$file" ] || fail "$file was not created"
  local size
  size=$(wc -c <"$file" | tr -d ' ')
  [ "$size" -ge "$min" ] || fail "$file is $size bytes; expected at least $min"
  head -c 5 "$file" | grep -q '%PDF-' || fail "$file does not start with %PDF-"
  echo "  ok $file ($size bytes)"
}

assert_contains() {
  local haystack=$1 needle=$2
  case "$haystack" in
    *"$needle"*) echo "  ok contains \"$needle\"" ;;
    *) fail "expected \"$needle\" in: $haystack" ;;
  esac
}

json_get() { python3 -c 'import json,sys; d=json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d = d[int(k)] if k.isdigit() else d[k]
print(d)' "$1"; }

# Every agentic sample sends `user_ref: quickstart`, and threads are keyed by
# `user_ref` — so without this the six of them run as one conversation, each
# picking up where the last left off.
#
# That is correct behaviour and wrong for a test harness: the first live run of
# these samples had a report directive arrive as the *third* turn of a thread
# that had already answered a chat question, and each sample's result depended
# on what the ones before it had said. See docs/coverage/api-contract.md §5.
#
# Deleting between samples is also what makes a *second* run deterministic:
# yesterday's thread would otherwise still be there tomorrow.
reset_threads() {
  local page ids
  page=$(curl -sS "$ARGENTUM_BASE_URL/v1/threads?user_ref=quickstart&limit=100" \
    -H "Authorization: Bearer $ARGENTUM_API_KEY") || return 0
  ids=$(printf '%s' "$page" | python3 -c 'import json,sys
for t in json.load(sys.stdin).get("data", []):
    print(t["id"])') || return 0
  for id in $ids; do
    curl -sS -o /dev/null -X DELETE "$ARGENTUM_BASE_URL/v1/threads/$id" \
      -H "Authorization: Bearer $ARGENTUM_API_KEY"
  done
  [ -n "$ids" ] && echo "  reset $(printf '%s\n' "$ids" | wc -l | tr -d ' ') quickstart thread(s)"
  return 0
}

# Runs an agentic sample, once more if the first attempt produced no document.
#
# **The retry is not flake-hiding.** The nightly run exists to catch a *broken
# sample* — a route renamed, a field removed, an SDK that no longer compiles —
# and the agentic door has one other way to produce nothing, which `T-A2`
# completes as a successful report with an absent `document`: the agent answers
# the prompt in prose without invoking `generate_document`. That is a model
# outcome, not a broken sample, and the retry is what keeps a nightly job from
# going red for it.
#
# It used to cover a second cause — our own `semantic_prompt_injection`
# guardrail refusing the report directive, four attempts in five — and this job
# was expected red until that was fixed. `T-A2b` fixed it on 2026-07-29 by
# moving the directive out of the user message, so the retry is back to
# covering only the ordinary reason. A sample that fails twice in a row is
# worth looking at; a run that *needs* the retry more than occasionally is
# worth looking at too.
retry_agentic() {
  reset_threads
  if "$@"; then return 0; fi
  printf '\n  first attempt produced no document; retrying once\n'
  reset_threads
  if "$@"; then
    printf '  the retry succeeded — the agent answered in prose on the first attempt\n'
    return 0
  fi
  fail "$* failed twice. Either the sample is broken, or the agent answered in prose on both attempts — read the thread to tell which."
}

cp "$here/spec.json" .

# -- curl ---------------------------------------------------------------------

run_curl_deterministic() {
  step "curl: GET /v1/me"
  me=$(bash -euo pipefail "$here/curl/me.sh")
  echo "$me"
  assert_contains "$me" '"api_version"'
  assert_contains "$me" '"scopes"'

  step "curl: POST /v1/reports/render"
  bash -euo pipefail "$here/curl/render.sh"
  assert_pdf revenue.pdf
  grep -qi '^x-request-id:' headers.txt || fail "no X-Request-Id on the render response"
  echo "  ok $(grep -i '^x-request-id:' headers.txt | tr -d '\r')"

  step "curl: GET /v1/documents"
  docs=$(bash -euo pipefail "$here/curl/documents.sh")
  assert_contains "$docs" '"has_more"'
  doc_id=$(printf '%s' "$docs" | json_get 'data.0.id')
  echo "  newest document: $doc_id"

  step "curl: GET /v1/documents/:id/content"
  curl -sS "$ARGENTUM_BASE_URL/v1/documents/$doc_id/content" \
    -H "Authorization: Bearer $ARGENTUM_API_KEY" -o downloaded.pdf
  assert_pdf downloaded.pdf

  step "curl: GET /v1/openapi.json — no credential at all"
  spec_status=$(curl -sS -o served-openapi.json -w '%{http_code}' "$ARGENTUM_BASE_URL/v1/openapi.json")
  [ "$spec_status" = "200" ] || fail "GET /v1/openapi.json answered $spec_status without a key"
  node "$repo/packages/openapi-tools/scripts/validate.mjs" served-openapi.json
}

curl_agentic_report() {
  local report report_id state status doc_id
  report=$(bash -euo pipefail "$here/curl/report.sh")
  echo "$report"
  report_id=$(printf '%s' "$report" | json_get id)
  status=""
  for _ in $(seq 1 90); do
    sleep 4
    state=$(curl -sS "$ARGENTUM_BASE_URL/v1/reports/$report_id" -H "Authorization: Bearer $ARGENTUM_API_KEY")
    status=$(printf '%s' "$state" | json_get status)
    echo "  $status"
    case "$status" in
      completed) break ;;
      failed) fail "report $report_id failed: $state" ;;
    esac
  done
  [ "$status" = "completed" ] || fail "report $report_id was still $status after six minutes"
  doc_id=$(printf '%s' "$state" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("document",{}).get("id",""))')
  if [ -z "$doc_id" ]; then
    echo "  report $report_id completed with no document"
    return 1
  fi
  curl -sS "$ARGENTUM_BASE_URL/v1/documents/$doc_id/content" \
    -H "Authorization: Bearer $ARGENTUM_API_KEY" -o agentic-curl.pdf
  assert_pdf agentic-curl.pdf
}

run_curl_agentic() {
  step "curl: POST /v1/reports — a real agent turn"
  retry_agentic curl_agentic_report

  step "curl: POST /v1/chat — streamed"
  reset_threads
  stream=$(bash -euo pipefail "$here/curl/chat.sh")
  printf '%s\n' "$stream" | head -20
  assert_contains "$stream" 'event: final'
}

# -- node ---------------------------------------------------------------------

setup_node() {
  step "node: install @argentum/sdk into an empty directory"
  (cd "$repo" && pnpm --filter @argentum/sdk build >/dev/null)
  tarball=$(cd "$repo/packages/argentum-node" && npm pack --silent --pack-destination "$work")
  mkdir -p "$work/node-app"
  cd "$work/node-app"
  [ -f package.json ] || npm init -y >/dev/null
  npm install --silent --no-audit --no-fund "$work/$tarball" >/dev/null
  # The samples are copied in rather than run from the repository. Node resolves
  # a bare import from the script's own directory upwards, so running
  # `node …/docs/api/examples/node/render.mjs` looks for @argentum/sdk next to
  # the repository and not next to the package we just installed — which is a
  # different thing from what the quickstart tells a reader to do, and it fails.
  cp "$here/spec.json" "$here"/node/*.mjs .
  echo "  installed $tarball"
}

run_node_deterministic() {
  setup_node
  step "node: reports.render"
  node render.mjs
  assert_pdf revenue-node.pdf
  cd "$work"
}

run_node_agentic() {
  setup_node
  step "node: reports.create → stream → download"
  retry_agentic node report.mjs
  assert_pdf agentic-node.pdf
  step "node: chat.stream"
  reset_threads
  node chat.mjs
  cd "$work"
}

# -- python -------------------------------------------------------------------

setup_python() {
  step "python: install argentum into a fresh virtualenv"
  mkdir -p "$work/python-app"
  cd "$work/python-app"
  [ -d venv ] || python3 -m venv venv
  ./venv/bin/pip install --quiet --upgrade pip >/dev/null
  ./venv/bin/pip install --quiet "$repo/packages/argentum-python" >/dev/null
  cp "$here/spec.json" "$here"/python/*.py .
  echo "  installed argentum $(./venv/bin/python -c 'import argentum; print(argentum.__version__)')"
}

run_python_deterministic() {
  setup_python
  step "python: reports.render"
  ./venv/bin/python render.py
  assert_pdf revenue-python.pdf
  cd "$work"
}

run_python_agentic() {
  setup_python
  step "python: reports.create → stream → download"
  retry_agentic ./venv/bin/python report.py
  assert_pdf agentic-python.pdf
  step "python: chat.stream"
  reset_threads
  ./venv/bin/python chat.py
  cd "$work"
}

case "$mode" in
  deterministic)
    run_curl_deterministic
    run_node_deterministic
    run_python_deterministic
    ;;
  agentic)
    run_curl_agentic
    run_node_agentic
    run_python_agentic
    ;;
  all)
    run_curl_deterministic
    run_node_deterministic
    run_python_deterministic
    run_curl_agentic
    run_node_agentic
    run_python_agentic
    ;;
  *)
    fail "unknown mode \"$mode\" — use deterministic, agentic or all"
    ;;
esac

printf '\nall %s samples passed in %ss (workspace %s)\n' "$mode" "$(( $(date +%s) - started ))" "$work"
