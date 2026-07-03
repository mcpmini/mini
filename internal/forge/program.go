package forge

import (
	"encoding/base64"
	"strings"
)

func buildProgram(code string, input []byte, marker string) string {
	moduleSrc := "export default (" + code + "\n);"
	codeB64 := base64.StdEncoding.EncodeToString([]byte(moduleSrc))

	inputLiteral := "null"
	if len(input) > 0 {
		inputLiteral = string(input)
	}

	replacer := strings.NewReplacer(
		"__MARKER__", marker,
		"__CODE_B64__", codeB64,
		"__INPUT_JSON__", inputLiteral,
	)
	return replacer.Replace(harnessTemplate)
}

// harnessTemplate is dynamic-imported as a data: URL so the user's code runs
// as its own module: a syntax error in it surfaces as a catchable TypeError
// (message contains "SyntaxError") rather than crashing the harness. The
// final write uses Deno.stdout.writeSync so the result marker can never be
// reordered behind buffered console.log output, and Deno.exit(0) right after
// kills any timers the user code left dangling. writeSync is not guaranteed
// to write the whole buffer in one call on a pipe (observed: 1 byte written
// of a 20MB payload before exit discarded the rest), so it runs in a loop
// over the remaining subarray until every byte is flushed.
const harnessTemplate = `
function __sanitize(text) {
  return String(text).replaceAll(/data:text\/typescript;base64,[A-Za-z0-9+/=]+/g, "code");
}

// The stack's first line already reads "Class: message", so the stack replaces the
// bare message; harness frames and the base64 data: URL are noise to the agent.
function __describe(e) {
  if (!e?.stack) return __sanitize(e?.message ?? e);
  return __sanitize(String(e.stack)
    .split("\n")
    .filter((l) => !l.includes("$deno$stdin"))
    .slice(0, 5)
    .join("\n"));
}

async function __run() {
  const __dataUrl = "data:text/typescript;base64,__CODE_B64__";
  let __mod;
  try {
    __mod = await import(__dataUrl);
  } catch (e) {
    const __msg = __sanitize(e?.message ?? e);
    const __kind = __msg.includes("SyntaxError") ? "syntax" : "runtime";
    return { error: { kind: __kind, message: __msg } };
  }

  const __fn = __mod.default;
  if (typeof __fn !== "function") {
    return { error: { kind: "runtime", message: "code did not evaluate to a function" } };
  }

  const __input = __INPUT_JSON__;
  let __result;
  try {
    __result = await __fn(__input);
  } catch (e) {
    let __msg = __describe(e);
    if (__msg.includes("--cached-only")) {
      __msg += " (this package must be listed in the packages parameter so the host can resolve it before execution)";
    }
    if (__msg.includes("--no-npm")) {
      __msg += " (packages must be declared in the packages parameter)";
    }
    if (__msg.includes("net access")) {
      __msg += " (host must be listed in code_mode.url_allow_list in mini's config)";
    }
    return { error: { kind: "runtime", message: __msg } };
  }
  if (__result === undefined) __result = null;

  try {
    const __json = JSON.stringify(__result);
    if (__json === undefined) {
      return { error: { kind: "not_serializable", message: "result is not JSON-serializable (e.g. a function or symbol)" } };
    }
    return { ok: JSON.parse(__json) };
  } catch (e) {
    return { error: { kind: "not_serializable", message: String(e?.message ?? e) } };
  }
}

let __payload = { error: { kind: "runner", message: "harness produced no result" } };
try {
  __payload = await __run();
} catch (e) {
  __payload = { error: { kind: "runtime", message: "harness error: " + String(e?.message ?? e) } };
} finally {
  const __out = "\n__MARKER__" + JSON.stringify(__payload);
  const __buf = new TextEncoder().encode(__out);
  let __off = 0;
  while (__off < __buf.length) { __off += Deno.stdout.writeSync(__buf.subarray(__off)); }
  Deno.exit(0);
}
`
