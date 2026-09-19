# toon

Encode-only [TOON](https://github.com/toon-format/spec) (Token-Oriented Object Notation) implementation, [spec v4.1](https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md) pinned to release [v4.1.1](https://github.com/toon-format/spec/tree/62f16b369408180f1faf1cba7da1b46d1f336f12).

TOON is a whitespace-structured format designed for LLM tool responses — uniform lists encode as compact tables, cutting token count by ~40% compared to JSON while remaining readable without a parser.

## Fixed encoding profile

| Option | Value | Configurable? |
|---|---|---|
| Delimiter | comma (`,`) | No |
| Indentation | 2 spaces | No |
| Key folding | removed in v4 | N/A |
| Depth limit | 1,024 levels | No |
| Output cap | 4 MiB | No |

## API

```go
// Convert any Go value → TOON string
v, err := toon.FromAny(myStruct)
encoded, err := toon.Encode(v)

// Convert raw JSON → TOON string
v, err := toon.FromJSON(rawJSON)
encoded, err := toon.Encode(v)
```

`FromAny` marshals via `encoding/json` then `FromJSON`, inheriting JSON's struct tag semantics. Non-finite floats (NaN, ±Inf) normalize to `null` per [spec §3](https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md#3-encoding-normalization-reference-encoder). Encoding errors are returned, never swallowed — there is no JSON fallback.

## Array forms

The encoder automatically selects the most compact representation:

- **Inline** — short primitive arrays: `[1, 2, 3]` ([§9.1](https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md#91-primitive-arrays--inline-form))
- **Tabular** — uniform object arrays, including nested field groups ([§9.3](https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md#93-arrays-of-objects--tabular-form)) and keyed tabular ([§9.5](https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md#95-objects-of-uniform-objects--keyed-tabular-form))
- **List** — mixed or non-uniform arrays ([§9.4](https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md#94-mixed-and-non-uniform-arrays--list-form))

## Safety limits

- **Depth**: pre-render validation rejects values nested beyond 1,024 levels before any output is written. Covers objects, arrays, and nested tabular recursion paths.
- **Output size**: encoding aborts at 4 MiB to bound indentation amplification from deeply nested non-tabular structures. Policy tracked in [#158](https://github.com/mcpmini/mini/issues/158).

## Vendored test fixtures

`testdata/spec/` contains the v4.1.1 encode fixture corpus, vendored from [toon-format/spec](https://github.com/toon-format/spec) at commit [`62f16b3`](https://github.com/toon-format/spec/tree/62f16b369408180f1faf1cba7da1b46d1f336f12). The upstream MIT license is included at `testdata/spec/LICENSE`. Provenance is documented in [`testdata/spec/README.md`](testdata/spec/README.md).

Conformance: 156 passed, 23 option-profile skips (configurable delimiter/indentation), 0 failures.

## License

The vendored test fixtures are copyright Johann Schopplich under the [MIT License](testdata/spec/LICENSE). The encoder implementation is part of mini, also MIT licensed.
