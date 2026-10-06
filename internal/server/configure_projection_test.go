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

func configCall(id int, args map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "config", "arguments": args}}
}

func getProjection(t *testing.T, srv *server.Server, sessionID, tool string) projectionReport {
	t.Helper()
	resp := postMCP(t, srv, sessionID, configCall(10, map[string]any{"action": "get_projection", "server": "svc", "tool": tool}))
	var report projectionReport
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &report); err != nil {
		t.Fatalf("get_projection %s: %v", tool, err)
	}
	return report
}

func newProjectionServer(t *testing.T, sessionID string, projections map[string]*config.ProjectionConfig) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Projections: projections}, fakeConn("getData", "other"))
	postMCP(t, srv, sessionID, initMsg(true))
	return srv
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
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000010"
	own := &config.ProjectionConfig{Exclude: []string{"secret"}}
	srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": own})

	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": own}})

	postMCP(t, srv, sessionID, setGetDataProjection(11, true))

	session := &config.ProjectionConfig{IncludeOnly: []string{"b"}}
	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"session": session, "server": own}})
}

func TestConfigureGetProjection_aToolWithoutRulesReportsNoRules(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000011"
	srv := newProjectionServer(t, sessionID, nil)

	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData"})
}

func TestConfigureGetProjection_acceptsTheAliasOrTheUpstreamName(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000012"
	rule := &config.ProjectionConfig{Alias: "fetch", Exclude: []string{"secret"}}
	srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": rule})

	assertReport(t, getProjection(t, srv, sessionID, "fetch"), projectionReport{Tool: "svc.fetch", Rules: map[string]*config.ProjectionConfig{"server": rule}})
	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": rule}})
}

func TestConfigureGetProjection_aRuleReadBackCanBeChangedWithoutLosingTheRest(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000013"
	srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": {Exclude: []string{"secret"}, StringLimits: map[string]int{"body": 100}}})

	rule := getProjection(t, srv, sessionID, "getData").Rules["server"]
	rule.StringLimits["body"] = 500
	saved := postMCP(t, srv, sessionID, configCall(11, map[string]any{"action": "set_projection", "server": "svc", "tool": "getData", "projection": rule}))
	if result, _ := saved["result"].(map[string]any); result == nil || result["isError"] == true {
		t.Fatalf("set_projection = %v", saved)
	}

	want := &config.ProjectionConfig{Exclude: []string{"secret"}, StringLimits: map[string]int{"body": 500}}
	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": want}})
}

func TestConfigureProjection_anAliasTheServerRejectedNamesTheRealTool(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000017"
	aliased, other := &config.ProjectionConfig{Alias: "other", Exclude: []string{"secret"}}, &config.ProjectionConfig{Exclude: []string{"b"}}
	srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": aliased, "other": other})

	assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other", Rules: map[string]*config.ProjectionConfig{"server": other}})

	postMCP(t, srv, sessionID, configCall(11, map[string]any{"action": "set_projection", "server": "svc", "tool": "other", "projection": map[string]any{"exclude": []string{"c"}}}))

	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": aliased}})
	assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other", Rules: map[string]*config.ProjectionConfig{"server": {Exclude: []string{"c"}}}})
}

func TestConfigureProjection_anAliasTwoToolsClaimNamesNoTool(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000018"
	first, second := &config.ProjectionConfig{Alias: "dup", Exclude: []string{"a"}}, &config.ProjectionConfig{Alias: "dup", Exclude: []string{"b"}}
	srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": first, "other": second})

	assertIsErrorResult(t, postMCP(t, srv, sessionID, configCall(11, map[string]any{"action": "get_projection", "server": "svc", "tool": "dup"})))
	postMCP(t, srv, sessionID, configCall(12, map[string]any{"action": "set_projection", "server": "svc", "tool": "dup", "projection": map[string]any{"exclude": []string{"c"}}}))

	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": first}})
	assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other", Rules: map[string]*config.ProjectionConfig{"server": second}})
}

func newServerAwaitingAuthorization(t *testing.T, sessionID string, projections map[string]*config.ProjectionConfig) *server.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(upstream.Close)
	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL, Projections: projections}
	dir := t.TempDir()
	configtest.WriteServer(t, dir, sc)
	srv := newTestServer(t, server.Params{ConfigDir: dir})
	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{sc})
	srv.WaitForStartupConnects()
	postMCP(t, srv, sessionID, initMsg(true))
	return srv
}

func TestConfigureGetProjection_readsAServerStillAwaitingAuthorization(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000015"
	rule := &config.ProjectionConfig{Alias: "fetch", Exclude: []string{"secret"}}
	srv := newServerAwaitingAuthorization(t, sessionID, map[string]*config.ProjectionConfig{"getData": rule})

	assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": rule}})
	assertReport(t, getProjection(t, srv, sessionID, "fetch"), projectionReport{Tool: "svc.fetch", Rules: map[string]*config.ProjectionConfig{"server": rule}})
	assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other"})
}

func TestConfigureProjection_beforeConnectingDropsTheAliasesTheServerWouldReject(t *testing.T) {
	t.Run("an alias that is another tool's name", func(t *testing.T) {
		const sessionID = "cccccccc-cccc-cccc-cccc-000000000019"
		aliased, other := &config.ProjectionConfig{Alias: "other", Exclude: []string{"secret"}}, &config.ProjectionConfig{Exclude: []string{"b"}}
		srv := newServerAwaitingAuthorization(t, sessionID, map[string]*config.ProjectionConfig{"getData": aliased, "other": other})

		assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other", Rules: map[string]*config.ProjectionConfig{"server": other}})
		postMCP(t, srv, sessionID, configCall(11, map[string]any{"action": "set_projection", "server": "svc", "tool": "other", "projection": map[string]any{"exclude": []string{"c"}}}))
		assertReport(t, getProjection(t, srv, sessionID, "getData"), projectionReport{Tool: "svc.getData", Rules: map[string]*config.ProjectionConfig{"server": aliased}})
		assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other", Rules: map[string]*config.ProjectionConfig{"server": {Exclude: []string{"c"}}}})
	})
	t.Run("an alias two tools claim", func(t *testing.T) {
		const sessionID = "cccccccc-cccc-cccc-cccc-000000000020"
		first, second := &config.ProjectionConfig{Alias: "dup", Exclude: []string{"a"}}, &config.ProjectionConfig{Alias: "dup", Exclude: []string{"b"}}
		srv := newServerAwaitingAuthorization(t, sessionID, map[string]*config.ProjectionConfig{"getData": first, "other": second})

		assertReport(t, getProjection(t, srv, sessionID, "dup"), projectionReport{Tool: "svc.dup"})
	})
}

func TestConfigureGetProjection_readsAHiddenTool(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000016"
	srv := newTestServer(t, server.Params{})
	hidden := &config.PermissionsConfig{Hidden: []string{"other", "getData"}}
	rule := &config.ProjectionConfig{Alias: "fetch", Exclude: []string{"secret"}}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: hidden, Projections: map[string]*config.ProjectionConfig{"getData": rule}}, fakeConn("getData", "other"))
	postMCP(t, srv, sessionID, initMsg(true))

	assertReport(t, getProjection(t, srv, sessionID, "other"), projectionReport{Tool: "svc.other"})
	assertReport(t, getProjection(t, srv, sessionID, "fetch"), projectionReport{Tool: "svc.fetch", Rules: map[string]*config.ProjectionConfig{"server": rule}})
}

func TestConfigureGetProjection_rejectsAMissingToolOrAnUnknownServerOrTool(t *testing.T) {
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000014"
	srv := newProjectionServer(t, sessionID, nil)
	cases := map[string]map[string]any{
		"missing tool":                 {"action": "get_projection", "server": "svc"},
		"unknown server":               {"action": "get_projection", "server": "nope", "tool": "getData"},
		"tool the server doesn't list": {"action": "get_projection", "server": "svc", "tool": "getDta"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			assertIsErrorResult(t, postMCP(t, srv, sessionID, configCall(12, args)))
		})
	}
}
