package tui

import (
	"slices"
	"testing"
)

func TestScroll_aViewScrolledToTheEndStaysFullWhenTheLinesShrink(t *testing.T) {
	sc := scroll{}
	sc.cut(cutParams{Lines: []string{"a", "b", "c", "d", "e", "f", "g", "h"}, First: 7, Last: 7, Height: 4})

	got := sc.cut(cutParams{Lines: []string{"a", "b", "c", "d", "e", "f"}, First: 5, Last: 5, Height: 4})

	if want := []string{"c", "d", "e", "f"}; !slices.Equal(got, want) {
		t.Errorf("view after the lines shrank = %v, want %v", got, want)
	}
}

func TestScroll_aBlockTallerThanTheWindowKeepsItsFirstLine(t *testing.T) {
	sc := scroll{}

	got := sc.cut(cutParams{Lines: []string{"a", "heading", "row", "url", "b"}, First: 1, Last: 3, Height: 2})

	if want := []string{"heading", "row"}; !slices.Equal(got, want) {
		t.Errorf("view of a 3-line block in a 2-line window = %v, want %v", got, want)
	}
}
