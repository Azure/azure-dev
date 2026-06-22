# Shared helpers for the Foundry private-networking E2E harness.
# Sourced by run-network-e2e.sh and assert-resources.sh; not executed directly.
#
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

# shellcheck shell=bash

set -Eeuo pipefail

# --- logging -----------------------------------------------------------------

log()  { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "${RUN_LOG:-/dev/null}"; }
info() { log "[info ] $*"; }
warn() { log "[warn ] $*" >&2; }
die()  { log "[fatal] $*" >&2; exit 1; }

# run_capture <name> <cmd...> : run a command, tee stdout+stderr+timing to
# $OUT_DIR/<name>.log, and still propagate failures. The command is time-capped
# (default STEP_TIMEOUT seconds, SIGKILL 30s after) so a silently-stuck Azure
# operation fails fast instead of hanging the run. Override per call with
# `STEP_TIMEOUT=<sec> run_capture ...`.
run_capture() {
  local name="$1"; shift
  local f="$OUT_DIR/$name.log"
  local t="${STEP_TIMEOUT:-1200}"
  info "==> $name: $* (timeout ${t}s)"
  local rc=0
  { time timeout -k 30 "$t" "$@"; } >"$f" 2>&1 || rc=$?
  if (( rc == 124 || rc == 137 )); then
    warn "$name TIMED OUT after ${t}s (rc=$rc; see $f)"; tail -n 40 "$f" >&2 || true; return 1
  elif (( rc != 0 )); then
    warn "$name FAILED (rc=$rc; see $f)"; tail -n 40 "$f" >&2 || true; return 1
  fi
  info "<== $name ok"
}

# run_retry <attempts> <name> <cmd...> : run_capture with retries, for ARM
# eventual-consistency transients (e.g. a create racing resource-group
# propagation). Backs off 10s between attempts.
run_retry() {
  local n="$1" name="$2"; shift 2
  local i
  for i in $(seq 1 "$n"); do
    if run_capture "$([[ $i -gt 1 ]] && echo "${name}-try$i" || echo "$name")" "$@"; then
      return 0
    fi
    (( i < n )) && { warn "$name attempt $i/$n failed; retrying in 10s"; sleep 10; }
  done
  return 1
}

# --- assertions --------------------------------------------------------------

assert_eq() { # <actual> <expected> <message>
  if [[ "$1" != "$2" ]]; then die "ASSERT $3: expected [$2] got [$1]"; fi
  info "ok: $3 == $2"
}

assert_contains() { # <haystack> <needle> <message>
  if [[ "$1" != *"$2"* ]]; then die "ASSERT $3: [$2] not found"; fi
  info "ok: $3 contains $2"
}

assert_ge() { # <actual> <min> <message>
  if (( $1 < $2 )); then die "ASSERT $3: expected >= $2 got $1"; fi
  info "ok: $3 ($1) >= $2"
}

# --- preflight ---------------------------------------------------------------

require_tools() {
  local t
  for t in az azd jq; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
  az account show >/dev/null 2>&1 || die "run 'az login' first"
  azd auth login --check-status >/dev/null 2>&1 || die "run 'azd auth login' first"
  # The 'ai agent' command group must be available (the eject step uses
  # `azd ai agent init --infra`).
  azd ai agent --help >/dev/null 2>&1 || die "azd 'ai agent' extension not available"
}

# --- azure.yaml mutation -----------------------------------------------------

# inject_network_block <azure.yaml path> : insert a network: block immediately
# after the foundry service's `host: azure.ai.agent` line, using the indentation
# that azd init emits (4 spaces under the service key). The block body is read
# from stdin and re-indented to 6 spaces.
inject_network_block() {
  local file="$1" tmp
  tmp="$(mktemp)"
  local block
  block="$(sed 's/^/      /')" # 6-space indent for keys under `    network:`
  awk -v blk="$block" '
    /^[[:space:]]+host:[[:space:]]+azure\.ai\.agent[[:space:]]*$/ {
      print
      print "    network:"
      print blk
      next
    }
    { print }
  ' "$file" >"$tmp"
  mv "$tmp" "$file"
}

# --- azd what-if parsing -----------------------------------------------------

# whatif_json <env dir> : run `azd provision --preview` and capture structured
# output. azd does not emit machine JSON for preview, so we keep the text log
# and grep it; callers assert on substrings.
preview_capture() { # <name>
  run_capture "$1" azd provision --preview --no-prompt
}
