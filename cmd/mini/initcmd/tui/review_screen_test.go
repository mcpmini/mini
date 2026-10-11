//go:build test

package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func reviewWith(added []string, endingInstead, offering []screen) *reviewScreen {
	s := newReviewScreen(reviewParams{
		added:         func() []string { return added },
		configDir:     "/example/mini",
		endingInstead: endingInstead,
		offering:      offering,
	})
	s.resize(200, 20)
	return s
}

func TestReview_listsTheServersAddedThisRun(t *testing.T) {
	s := reviewWith([]string{"linear", "sentry"}, nil, []screen{&fakeScreen{}})
	if body := s.body(10, false); !strings.Contains(body, "will be added to mini: linear, sentry") {
		t.Errorf("body:\n%s\nwant the servers this run adds named", body)
	}
}

func TestReview_withNothingAddedPointsAtTheServersDir(t *testing.T) {
	s := reviewWith(nil, nil, []screen{&fakeScreen{}})
	body := s.body(10, false)
	if !strings.Contains(body, "No MCPs will be added to mini.") || !strings.Contains(body, "/example/mini/servers") {
		t.Errorf("body:\n%s\nwant the no-MCPs line and the servers dir", body)
	}
}

func TestReview_isEmptyWhileAScreenThatEndsTheRunInsteadShows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		endingInstead []screen
		want          bool
	}{
		{"none shows", []screen{&fakeScreen{nothing: true}}, false},
		{"one shows", []screen{&fakeScreen{}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reviewWith(nil, tc.endingInstead, []screen{&fakeScreen{}})
			if got := s.empty(); got != tc.want {
				t.Errorf("empty() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRun_aRunWithAnAgentToConnectEndsOnConnectNotReview(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	setup := connectingSetup(configWithAServerToFinish(t), stdioAgent("Codex"))
	view := screenAfter(t, setup, fromCatalog(c), "tab", "enter", "tab", "enter", "down", "enter")
	if !strings.Contains(view, "Connect mini to Codex") || strings.Contains(view, "Review and finish") {
		t.Errorf(
			"after choosing on Connect:\n%s\nwant the run ended on Connect: Review is only for runs without agents",
			view,
		)
	}
}

func TestRun_aRunThatOnlyImportsEndsOnReview(t *testing.T) {
	view := screenAfter(
		t,
		setupFor(t.TempDir(), stdioAgent("Codex", "files")),
		fromCatalog(catalog.Catalog{}),
		"tab",
		"enter",
	)
	if !strings.Contains(view, "Review and finish") || !strings.Contains(view, "will be added to mini: files") {
		t.Errorf("after Continue on Import:\n%s\nwant Review naming files", view)
	}
}

func TestReview_isEmptyOnlyWhenNothingWasOffered(t *testing.T) {
	t.Run("nothing offered by Import or Catalog", func(t *testing.T) {
		s := reviewWith(nil, nil, []screen{&fakeScreen{nothing: true}, &fakeScreen{nothing: true}})
		if !s.empty() {
			t.Error("empty() = false, want true: with nothing offered there is nothing to review")
		}
	})
	t.Run("Catalog offered something", func(t *testing.T) {
		s := reviewWith(nil, nil, []screen{&fakeScreen{nothing: true}, &fakeScreen{}})
		if s.empty() {
			t.Error("empty() = true, want false: Catalog offered servers, so Review shows")
		}
	})
}

func TestRun_noAgentsWithAnOAuthPickGoesFromLoginsToReview(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	logins := screenAfter(t, setupFor(t.TempDir()), fromCatalog(c), "space", "tab", "enter")
	if !strings.Contains(logins, "Log in to your MCPs (optional)") || strings.Contains(logins, "Save and finish") {
		t.Errorf("Logins:\n%s\nwant Continue, not the final actions: Review ends the run", logins)
	}
	review := screenAfter(t, setupFor(t.TempDir()), fromCatalog(c), "space", "tab", "enter", "tab", "enter")
	if !strings.Contains(review, "Review and finish") ||
		!strings.Contains(review, "Save and finish\n  Exit without saving\n  Back") {
		t.Errorf("after Continue on Logins:\n%s\nwant Review with the final actions", review)
	}
}

func TestRun_noAgentsWithANoAuthPickReachesReview(t *testing.T) {
	reviewAfterNoAuthPick(t, t.TempDir(), func(m tea.Model) {
		if view := shown(m.(*app)); !strings.Contains(view, "Review and finish") ||
			!strings.Contains(view, "will be added to mini: sentry") {
			t.Errorf("view:\n%s\nwant Review listing sentry as added", view)
		}
	})
}

// The probe ends sentry's check at once, so Logins empties and Continue reaches Review.
func reviewAfterNoAuthPick(t *testing.T, configDir string, onReview func(m tea.Model)) Outcome {
	t.Helper()
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	setup := setupFor(configDir)
	setup.Probe = func(context.Context, string, config.ServerConfig) error { return nil }
	program := func(m tea.Model) error {
		pressing("space", "tab")(m)
		deliver(m, pressKey(m, "enter"))
		pressKey(m, "enter")
		onReview(m)
		return nil
	}
	out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(c), Program: program})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRun_saveAndFinishOnReviewWritesTheTicks(t *testing.T) {
	configDir := t.TempDir()
	out := reviewAfterNoAuthPick(t, configDir, func(m tea.Model) {
		pressKey(m, "enter")
	})
	if !out.Saved {
		t.Fatal("Run returned no saved outcome, want Save and finish on Review to finish the run")
	}
	if got := writtenNames(t, configDir); !slices.Equal(got, []string{"sentry"}) {
		t.Errorf("written = %v, want sentry: it was ticked and Review saved it", got)
	}
}

func TestRun_exitWithoutSavingOnReviewWritesNothing(t *testing.T) {
	configDir := t.TempDir()
	out := reviewAfterNoAuthPick(t, configDir, func(m tea.Model) {
		pressKey(m, "down")
		pressKey(m, "enter")
	})
	if out.Saved {
		t.Error("Run returned a saved outcome, want Exit without saving to end the run unsaved")
	}
	if got := writtenNames(t, configDir); len(got) != 0 {
		t.Errorf("written = %v, want nothing: Exit without saving writes no servers", got)
	}
}

func TestRun_catalogOfferedButNothingTickedReviewsWithTheGuidance(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	view := screenAfter(t, setupFor(t.TempDir()), fromCatalog(c), "tab", "enter")
	if !strings.Contains(view, "Review and finish") || !strings.Contains(view, "No MCPs will be added to mini.") {
		t.Errorf("view:\n%s\nwant Review with the no-MCPs guidance", view)
	}
}
