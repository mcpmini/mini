package toon

import (
	"regexp"
	"strings"
)

// identifierSegmentRE is spec §1.9's IdentifierSegment: stricter than
// unquotedKeyRE (no dots), used only for folding eligibility.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#19-key-folding-and-path-expansion-terms
var identifierSegmentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// foldValue applies §13.4 key folding in safe mode with unlimited depth,
// mini's locked configuration. It rewrites the Value tree before rendering
// so folded chains participate in tabular detection.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#134-key-folding-and-path-expansion
func foldValue(v Value) Value {
	switch v.Kind {
	case KindObject:
		return foldObject(v)
	case KindArray:
		items := make([]Value, len(v.Items))
		for i, it := range v.Items {
			items[i] = foldValue(it)
		}
		return Value{Kind: KindArray, Items: items}
	default:
		return v
	}
}

func foldObject(v Value) Value {
	fields := make([]Field, len(v.Fields))
	for i, f := range v.Fields {
		fields[i] = foldField(f, v.Fields)
	}
	return Value{Kind: KindObject, Fields: fields}
}

func foldField(f Field, siblings []Field) Field {
	segs, leaf, foldable := foldableChain(f)
	if foldable && chainIsSafe(segs, siblings) {
		return Field{Key: strings.Join(segs, "."), Val: foldValue(leaf)}
	}
	return Field{Key: f.Key, Val: foldChainTail(f.Val)}
}

func foldableChain(f Field) (segs []string, leaf Value, foldable bool) {
	segs = []string{f.Key}
	leaf = f.Val
	for leaf.Kind == KindObject && len(leaf.Fields) == 1 {
		segs = append(segs, leaf.Fields[0].Key)
		leaf = leaf.Fields[0].Val
	}
	validLeaf := leaf.Kind != KindObject || len(leaf.Fields) == 0
	return segs, leaf, len(segs) >= 2 && validLeaf
}

func chainIsSafe(segs []string, siblings []Field) bool {
	for _, s := range segs {
		if !identifierSegmentRE.MatchString(s) {
			return false
		}
	}
	folded := strings.Join(segs, ".")
	for _, sib := range siblings {
		if sib.Key == folded {
			return false
		}
	}
	return true
}

// foldChainTail renders the remainder of a chain that was not folded: per the
// spec's standard-nesting expectation for skipped chains, nothing along the
// single-key spine folds, while values beyond it start fresh chains.
func foldChainTail(v Value) Value {
	if v.Kind == KindObject && len(v.Fields) == 1 {
		inner := v.Fields[0]
		return Value{Kind: KindObject, Fields: []Field{{Key: inner.Key, Val: foldChainTail(inner.Val)}}}
	}
	return foldValue(v)
}
