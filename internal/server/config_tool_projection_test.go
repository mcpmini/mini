//go:build test

package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
)

type projectionReport struct {
	Tool  string                              `json:"tool"`
	Rules map[string]*config.ProjectionConfig `json:"rules"`
}

type rulesByScope = map[string]*config.ProjectionConfig

func serverRule(rule *config.ProjectionConfig) rulesByScope {
	return rulesByScope{"server": rule}
}

func openSession(t *testing.T, srv *server.Server) *httpSession {
	t.Helper()
	return newProxyHarness(t, srv).session("cccccccc-cccc-cccc-cccc-000000000001")
}

func newProjectionServer(t *testing.T, sc config.ServerConfig) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("getData", "other")
	conn.Responses["tools/call"] = json.RawMessage(textResult(`{"a":1,"b":2}`))
	addEdgeConn(addConnectionParams{T: t, Server: srv, Config: sc, Connection: conn})
	return srv
}

func newProjectionSession(t *testing.T, rules rulesByScope) *httpSession {
	t.Helper()
	return openSession(t, newProjectionServer(t, config.ServerConfig{Name: "svc", Projections: rules}))
}

func (s *httpSession) configure(args map[string]any) map[string]any {
	s.h.t.Helper()
	return s.call("config", args)
}

func (s *httpSession) saveProjection(tool string, projection any) map[string]any {
	s.h.t.Helper()
	args := map[string]any{"action": "set_projection", "server": "svc", "tool": tool, "projection": projection}
	return s.configure(args)
}

func (s *httpSession) projection(tool string) projectionReport {
	s.h.t.Helper()
	args := map[string]any{"action": "get_projection", "server": "svc", "tool": tool}
	text := toolResultText(s.h.t, s.configure(args))
	var report projectionReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		s.h.t.Fatalf("get_projection %s: %v", tool, err)
	}
	return report
}

func (s *httpSession) expectRules(tool string, rules rulesByScope) {
	s.h.t.Helper()
	assertReport(s.h.t, s.projection(tool), projectionReport{Tool: "svc." + tool, Rules: rules})
}

func (s *httpSession) expectGetDataFields(present, absent []string) {
	s.h.t.Helper()
	text := s.callText("svc__getData", nil)
	assertProjectedFields(assertProjectedFieldsParams{T: s.h.t, Text: text, Present: present, Absent: absent})
}

func assertReport(t *testing.T, got, want projectionReport) {
	t.Helper()
	if len(want.Rules) == 0 && len(got.Rules) == 0 {
		got.Rules, want.Rules = nil, nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("get_projection = %+v, want %+v", got, want)
	}
}

func TestConfigureGetProjection_reportsTheServerRuleAndThisSessionsOverride(t *testing.T) {
	own := &config.ProjectionConfig{Exclude: []string{"secret"}}
	s := newProjectionSession(t, rulesByScope{"getData": own})

	s.expectRules("getData", serverRule(own))

	s.configure(includeOnlyB(true))

	session := &config.ProjectionConfig{IncludeOnly: []string{"b"}}
	s.expectRules("getData", rulesByScope{"session": session, "server": own})
}

func TestConfigureGetProjection_aToolWithoutRulesReportsNoRules(t *testing.T) {
	newProjectionSession(t, nil).expectRules("getData", nil)
}

func TestConfigureGetProjection_acceptsTheAliasOrTheUpstreamName(t *testing.T) {
	rule := &config.ProjectionConfig{Alias: "fetch", Exclude: []string{"secret"}}
	s := newProjectionSession(t, rulesByScope{"getData": rule})

	s.expectRules("fetch", serverRule(rule))
	s.expectRules("getData", serverRule(rule))
}

func TestConfigureGetProjection_aRuleReadBackCanBeChangedWithoutLosingTheRest(t *testing.T) {
	s := newProjectionSession(t, rulesByScope{
		"getData": {Exclude: []string{"secret"}, StringLimits: map[string]int{"body": 100}},
	})

	rule := s.projection("getData").Rules["server"]
	rule.StringLimits["body"] = 500
	successText(t, s.saveProjection("getData", rule))

	s.expectRules("getData", serverRule(
		&config.ProjectionConfig{Exclude: []string{"secret"}, StringLimits: map[string]int{"body": 500}},
	))
}

func TestConfigureProjection_anAliasTheServerRejectedNamesTheRealTool(t *testing.T) {
	aliased := &config.ProjectionConfig{Alias: "other", Exclude: []string{"secret"}}
	other := &config.ProjectionConfig{Exclude: []string{"b"}}
	s := newProjectionSession(t, rulesByScope{"getData": aliased, "other": other})

	s.expectRules("other", serverRule(other))

	s.saveProjection("other", map[string]any{"exclude": []string{"c"}})

	s.expectRules("getData", serverRule(aliased))
	s.expectRules("other", serverRule(&config.ProjectionConfig{Exclude: []string{"c"}}))
}

func TestConfigureProjection_anAliasTwoToolsClaimNamesNoTool(t *testing.T) {
	first := &config.ProjectionConfig{Alias: "dup", Exclude: []string{"a"}}
	second := &config.ProjectionConfig{Alias: "dup", Exclude: []string{"b"}}
	s := newProjectionSession(t, rulesByScope{"getData": first, "other": second})

	assertIsErrorResult(t, s.configure(map[string]any{"action": "get_projection", "server": "svc", "tool": "dup"}))
	s.saveProjection("dup", map[string]any{"exclude": []string{"c"}})

	s.expectRules("getData", serverRule(first))
	s.expectRules("other", serverRule(second))
}

func newServerAwaitingAuthorization(t *testing.T, rules rulesByScope) *server.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(upstream.Close)
	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL, Projections: rules}
	dir := t.TempDir()
	configtest.WriteServer(t, dir, sc)
	srv := newTestServer(t, server.Params{ConfigDir: dir})
	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{sc})
	srv.WaitForStartupConnects()
	return srv
}

func TestConfigureGetProjection_readsAServerStillAwaitingAuthorization(t *testing.T) {
	rule := &config.ProjectionConfig{Alias: "fetch", Exclude: []string{"secret"}}
	s := openSession(t, newServerAwaitingAuthorization(t, rulesByScope{"getData": rule}))

	s.expectRules("getData", serverRule(rule))
	s.expectRules("fetch", serverRule(rule))
	s.expectRules("other", nil)
}

func TestConfigureProjection_beforeConnectingDropsTheAliasesTheServerWouldReject(t *testing.T) {
	t.Run("an alias that is another tool's name", func(t *testing.T) {
		aliased := &config.ProjectionConfig{Alias: "other", Exclude: []string{"secret"}}
		other := &config.ProjectionConfig{Exclude: []string{"b"}}
		s := openSession(t, newServerAwaitingAuthorization(t, rulesByScope{"getData": aliased, "other": other}))

		s.expectRules("other", serverRule(other))
		s.saveProjection("other", map[string]any{"exclude": []string{"c"}})
		s.expectRules("getData", serverRule(aliased))
		s.expectRules("other", serverRule(&config.ProjectionConfig{Exclude: []string{"c"}}))
	})
	t.Run("an alias two tools claim", func(t *testing.T) {
		first := &config.ProjectionConfig{Alias: "dup", Exclude: []string{"a"}}
		second := &config.ProjectionConfig{Alias: "dup", Exclude: []string{"b"}}
		s := openSession(t, newServerAwaitingAuthorization(t, rulesByScope{"getData": first, "other": second}))

		s.expectRules("dup", nil)
	})
}

func TestConfigureGetProjection_readsAHiddenTool(t *testing.T) {
	rule := &config.ProjectionConfig{Alias: "fetch", Exclude: []string{"secret"}}
	s := openSession(t, newProjectionServer(t, config.ServerConfig{
		Name:        "svc",
		Permissions: &config.PermissionsConfig{Hidden: []string{"other", "getData"}},
		Projections: rulesByScope{"getData": rule},
	}))

	s.expectRules("other", nil)
	s.expectRules("fetch", serverRule(rule))
}

func TestConfigureGetProjection_rejectsAMissingToolOrAnUnknownServerOrTool(t *testing.T) {
	cases := map[string]map[string]any{
		"missing tool":                 {"action": "get_projection", "server": "svc"},
		"unknown server":               {"action": "get_projection", "server": "nope", "tool": "getData"},
		"tool the server doesn't list": {"action": "get_projection", "server": "svc", "tool": "getDta"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			assertIsErrorResult(t, newProjectionSession(t, nil).configure(args))
		})
	}
}
