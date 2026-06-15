#!/usr/bin/env bash
# scripts/pre-release.sh — run this before tagging any release.
#
# Usage:
#   ./scripts/pre-release.sh v0.1.0
#
# What it does (in order):
#   1.  Verify working tree is clean
#   2.  Build + lint + race tests (check.sh)
#   3.  Integration tests
#   4.  Build release binary with version injected via -ldflags; verify output
#   5.  CLI smoke tests against the release binary
#   6.  Benchmarks (projection savings — eyeball for regressions)
#   7.  Manual checklist prompt
#   8.  Evals (optional — costs ~$2-5 in API tokens)
#   9.  Create git tag and print push instructions

set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "usage: $0 <version>   e.g. $0 v0.1.0"
  exit 1
fi
# Accept v0.1.0 or 0.1.0
SEMVER="${VERSION#v}"
TAG="v${SEMVER}"

export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"
LDFLAGS="-X github.com/mcpmini/mini/internal/version.buildRevision=${SEMVER}"
BIN=/tmp/mini-release-${SEMVER}

step()  { echo; echo "▶  $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*"; exit 1; }
ask()   { local prompt="$1" var; read -rp "  ${prompt} [y/N] " var; [[ "${var,,}" == "y" ]]; }

# ── 1. Git state ──────────────────────────────────────────────────────────────
step "checking git state"
if [[ -n "$(git status --porcelain)" ]]; then
  fail "working tree is dirty — commit or stash changes first"
fi
if git rev-parse "$TAG" &>/dev/null; then
  fail "tag $TAG already exists"
fi
ok "working tree clean, tag $TAG available"

# ── 2. Build + lint + race tests ─────────────────────────────────────────────
step "build + lint + race tests (check.sh)"
./check.sh
ok "check.sh passed"

# ── 3. Integration tests ──────────────────────────────────────────────────────
step "integration tests"
go test -tags integration,test ./test/integration/... -timeout 180s
ok "integration tests passed"

# ── 4. Release binary ─────────────────────────────────────────────────────────
step "building release binary (version ${SEMVER})"
go build -ldflags "$LDFLAGS" -o "$BIN" ./cmd/mini
GOT=$("$BIN" --version)
if [[ "$GOT" != "$SEMVER" ]]; then
  fail "binary reports '${GOT}', expected '${SEMVER}'"
fi
ok "binary version verified: ${GOT}  →  ${BIN}"

# ── 5. CLI smoke tests ────────────────────────────────────────────────────────
step "CLI smoke tests"
CFG=$(mktemp -d)
"$BIN" --config "$CFG" ls | grep -q "no servers" || fail "empty ls failed"
"$BIN" --config "$CFG" add smoketest --url https://httpbin.org/anything
"$BIN" --config "$CFG" ls | grep -q "smoketest" || fail "server not listed after add"
"$BIN" --config "$CFG" rm smoketest
"$BIN" --config "$CFG" ls | grep -q "no servers" || fail "server still listed after rm"
# Verify --version / version subcommand
"$BIN" --version | grep -q "$SEMVER" || fail "--version output wrong"
# Verify bad server name is rejected
"$BIN" --config "$CFG" add "bad/name" --url https://x.com 2>&1 | grep -qi "invalid" || fail "bad server name not rejected"
ok "smoke tests passed"

# ── 6. Benchmarks ─────────────────────────────────────────────────────────────
step "benchmarks (review for regressions — target ≥40% savings on GitHub fixtures)"
go test -tags test -bench=. -benchtime=3s ./internal/projection/... ./internal/server/... 2>&1 \
  | grep -E "^Benchmark"
ok "benchmarks done"

# ── 7. Manual checklist ───────────────────────────────────────────────────────
step "manual checklist"
cat <<'EOF'

  Work through each item, then confirm below.

  Protocol
  [ ] mini serve --standalone responds to initialize + tools/list over stdio
  [ ] tools/call routes correctly to a real upstream (use mini call or Claude)

  Proxy mode
  [ ] mini_config status returns server health
  [ ] mini_read returns file contents for a large response written to disk
  [ ] Cross-origin POST to /mcp returns 403

  Auth
  [ ] mini auth <server> starts PKCE flow and prints a URL
  [ ] Token file created at ~/.mini/tokens/<server>.json with 0600 permissions

  Response store
  [ ] ~/.mini/responses/ created with 0700 permissions
  [ ] Large tool response writes a file (inline:false) and file path is readable

  ROADMAP / SECURITY
  [ ] ROADMAP.md v0.1 capabilities list is accurate
  [ ] SECURITY.md claims match current code

  Docs
  [ ] README.md install instructions are accurate for this version
  [ ] No new TODO/FIXME/HACK introduced since last release (git grep -i "todo\|fixme\|hack")

EOF
ask "All manual checks passed?" || { echo "  Aborted — re-run after completing checks."; exit 1; }
ok "manual checks confirmed"

# ── 8. Evals (optional) ───────────────────────────────────────────────────────
echo
if ask "Run evals? (~\$2–5 in API tokens, ~10 min)"; then
  step "running evals"
  ./scripts/run_evals.sh
  ok "evals passed"
else
  echo "  (evals skipped)"
fi

# ── 9. Tag ────────────────────────────────────────────────────────────────────
step "tagging ${TAG}"
echo
if ask "Create tag ${TAG}?"; then
  git tag "$TAG"
  echo
  echo "  Tagged. Push with:"
  echo "    git push origin ${TAG}"
  echo
  ok "release ${TAG} ready — push the tag when ready."
else
  echo "  Tag skipped. Create manually with: git tag ${TAG}"
fi
