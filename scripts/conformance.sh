#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
results=${CONFORMANCE_RESULTS:-$(mktemp -d "${TMPDIR:-/tmp}/mini-conformance-results.XXXXXX")}
build=$(mktemp -d "${TMPDIR:-/tmp}/mini-conformance-build.XXXXXX")
trap 'rm -rf "$build"' EXIT
mkdir -p "$results"

go build -o "$build/mini" "$root/cmd/mini"
go build -o "$build/conformanceclient" "$root/test/conformanceclient"
export MINI_BIN="$build/mini"

agent_2026=(tools_call request-metadata http-standard-headers http-custom-headers http-invalid-tool-headers json-schema-ref-no-deref json-schema-2020-12-preservation sep-2322-client-request-state)
cli_2026=(tools_call request-metadata http-standard-headers http-custom-headers http-invalid-tool-headers json-schema-ref-no-deref)
auth_2026=(auth/metadata-default auth/metadata-var1 auth/metadata-var2 auth/metadata-var3 auth/basic-cimd auth/scope-from-www-authenticate auth/scope-from-scopes-supported auth/scope-omitted-when-undefined auth/scope-step-up auth/scope-retry-limit auth/token-endpoint-auth-basic auth/token-endpoint-auth-post auth/token-endpoint-auth-none auth/pre-registration auth/resource-mismatch auth/offline-access-scope auth/offline-access-not-supported auth/authorization-server-migration auth/iss-supported auth/iss-not-advertised auth/iss-supported-missing auth/iss-wrong-issuer auth/iss-unexpected auth/iss-normalized auth/metadata-issuer-mismatch)
legacy_2025=(initialize tools_call json-schema-2020-12-preservation)

run_set() {
  local set=$1 mode=$2 version=$3 baseline=$4
  shift 4
  export MINI_CONFORMANCE_MODE=$mode
  local failures=0 scenario output status
  # Serial on purpose: mini's OAuth callback listens on fixed port 6464, so auth scenarios can't overlap.
  for scenario in "$@"; do
    local output_dir="$results/$set/$scenario"
    mkdir -p "$output_dir"
    if output=$("$root/test/conformance/node_modules/.bin/conformance" client \
      --command "$build/conformanceclient" --scenario "$scenario" --spec-version "$version" \
      --expected-failures "$root/$baseline" --output-dir "$output_dir" 2>&1); then
      status=0
    else
      status=$?
      failures=$((failures + 1))
    fi
    printf '%s\n' "$output" > "$output_dir/output.log"
    printf '%s: %s (exit %d)\n' "$set" "$scenario" "$status"
  done
  ((failures == 0))
}

sets=(agent cli auth legacy)
if [[ $# -gt 1 ]]; then
  printf 'usage: %s [agent|cli|auth|legacy]\n' "$0" >&2
  exit 2
fi
if [[ $# -eq 1 ]]; then
  sets=("$1")
fi

failed=0
for set in "${sets[@]}"; do
  case "$set" in
    agent) run_set agent agent 2026-07-28 test/conformance/agent.baseline.yml "${agent_2026[@]}" || failed=1 ;;
    cli) run_set cli cli 2026-07-28 test/conformance/cli.baseline.yml "${cli_2026[@]}" || failed=1 ;;
    auth) run_set auth cli 2026-07-28 test/conformance/auth.baseline.yml "${auth_2026[@]}" || failed=1 ;;
    legacy) run_set legacy agent 2025-11-25 test/conformance/legacy.baseline.yml "${legacy_2025[@]}" || failed=1 ;;
    *) printf 'unknown conformance set: %s\n' "$set" >&2; exit 2 ;;
  esac
done
printf 'Conformance results: %s\n' "$results"
exit "$failed"
