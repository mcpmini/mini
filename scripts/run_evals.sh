#!/usr/bin/env bash
# Run each eval in a separate process so they don't consume each other's
# timeout budget.  Output is buffered per-eval and printed in order after
# all processes finish, so results are readable even when running in parallel.
#
# Usage:
#   ./scripts/run_evals.sh                  # run all evals
#   ./scripts/run_evals.sh TestEval_Hello   # run a single named eval
#
# EVAL_TIMEOUT controls the per-eval timeout (default 300s).

set -uo pipefail

TIMEOUT=${EVAL_TIMEOUT:-300s}

EVALS=(
    TestEval_Hello
    TestEval_BugFixPipeline
    TestEval_IncidentTriage
    TestEval_ReviewPRs
)

# If a specific eval is requested, run only that one.
if [ "${1:-}" != "" ]; then
    EVALS=("$1")
fi

# Build the test binary once so parallel workers don't race the compiler.
# mini and fakemcp are still built per-worker (fast due to build cache).
BIN=$(mktemp -d)/mini-evals
echo "building eval binary…"
if ! go test -tags evals -c -o "$BIN" ./evals/ 2>&1; then
    echo "build failed"
    exit 1
fi
echo "done"
echo ""

pids=()
logs=()

for name in "${EVALS[@]}"; do
    log=$(mktemp /tmp/mini-eval-XXXXXX)
    logs+=("$log")
    "$BIN" -test.run "^${name}$" -test.v -test.timeout "$TIMEOUT" -test.count 1 \
        >"$log" 2>&1 &
    pids+=($!)
    printf "  started %-40s pid %d\n" "$name" "$!"
done

echo ""
echo "waiting for ${#pids[@]} eval(s)…"
echo ""

failures=0
for i in "${!pids[@]}"; do
    pid=${pids[$i]}
    name=${EVALS[$i]}
    log=${logs[$i]}

    if wait "$pid"; then
        label="PASS"
    else
        label="FAIL"
        failures=$(( failures + 1 ))
    fi

    echo "┌─── $name  →  $label"
    sed 's/^/│ /' "$log"
    echo "└────────────────────────────────────────────"
    echo ""
    rm -f "$log"
done

if [ "$failures" -gt 0 ]; then
    echo "$failures eval(s) FAILED"
    exit 1
fi
echo "all evals passed"
