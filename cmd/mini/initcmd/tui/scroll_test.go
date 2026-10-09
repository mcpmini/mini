package tui

import (
	"slices"
	"testing"
)

func TestScroll_aViewScrolledToTheEndStaysFullWhenTheLinesShrink(t *testing.T) {
	sc := scroll{}
	sc.cut([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, 7, 1, 4)

	got := sc.cut([]string{"a", "b", "c", "d", "e", "f"}, 5, 1, 4)

	if want := []string{"c", "d", "e", "f"}; !slices.Equal(got, want) {
		t.Errorf("view after the lines shrank = %v, want %v", got, want)
	}
}

func TestScroll_aBlockTallerThanTheWindowKeepsItsFirstLine(t *testing.T) {
	sc := scroll{}

	got := sc.cut([]string{"a", "heading", "row", "url", "b"}, 1, 3, 2)

	if want := []string{"heading", "row"}; !slices.Equal(got, want) {
		t.Errorf("view of a 3-line block in a 2-line window = %v, want %v", got, want)
	}
}
