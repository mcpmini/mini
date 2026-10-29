package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const maxAuthBodyBytes = 64 << 10

type protectedResourceMeta struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type prmFetchParams struct {
	MetadataURL      string
	ExpectedResource string
}

type prmDiscovery struct {
	Origin     string
	Candidates []prmFetchParams
}

func requireHTTPURL(rawURL string) error {
	_, err := parseHTTPURL(rawURL)
	return err
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP(S) URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported protocol scheme %q in URL", u.Scheme)
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("URL must be an absolute HTTP(S) URL without userinfo or fragment")
	}
	return u, nil
}

func parsePRMDiscovery(serverURL string) (prmDiscovery, error) {
	u, err := parseHTTPURL(serverURL)
	if err != nil {
		return prmDiscovery{}, fmt.Errorf("parse server URL: %w", err)
	}
	return prmDiscoveryForURL(u, serverURL), nil
}

func prmDiscoveryForURL(u *url.URL, serverURL string) prmDiscovery {
	base := u.Scheme + "://" + u.Host
	path := u.EscapedPath()
	if path == "/" {
		path = ""
	}
	metadataURL := base + "/.well-known/oauth-protected-resource" + path
	if u.RawQuery != "" {
		metadataURL += "?" + u.RawQuery
	}
	candidates := []prmFetchParams{{MetadataURL: metadataURL, ExpectedResource: serverURL}}
	if path != "" || u.RawQuery != "" {
		candidates = append(candidates, prmFetchParams{
			MetadataURL:      base + "/.well-known/oauth-protected-resource",
			ExpectedResource: serverURL,
		})
	}
	return prmDiscovery{Origin: base, Candidates: candidates}
}

func (f *oauthHTTPFlow) probePRMCandidates(ctx context.Context, discovery prmDiscovery) (asRef, error) {
	var transientErr error
	for _, candidate := range discovery.Candidates {
		ref, err := f.fetchASURLFromPRM(ctx, candidate)
		if err != nil {
			transientErr = err
			continue
		}
		if ref.URL != "" {
			return ref, nil
		}
	}
	if transientErr != nil {
		return asRef{}, transientErr
	}
	return asRef{URL: discovery.Origin}, nil
}

func (f *oauthHTTPFlow) fetchASURLFromPRM(ctx context.Context, p prmFetchParams) (asRef, error) {
	resp, err := f.doDiscoveryRequest(ctx, p.MetadataURL)
	if err != nil {
		if ctx.Err() != nil {
			return asRef{}, ctx.Err()
		}
		return asRef{}, fmt.Errorf("fetch protected resource metadata from %s: %w", p.MetadataURL, err)
	}
	//nolint:errcheck // status and metadata determine the result; Close releases the response stream.
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return asRef{}, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return asRef{}, fmt.Errorf(
			"fetch protected resource metadata: status %d from %s",
			resp.StatusCode,
			p.MetadataURL,
		)
	}
	if resp.StatusCode != http.StatusOK {
		return asRef{}, nil
	}
	return decodePRM(resp, p)
}

func decodePRM(resp *http.Response, p prmFetchParams) (asRef, error) {
	var meta protectedResourceMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&meta); err != nil {
		// Catch-all hosts answer every path with a 200 HTML page; only a body labeled JSON is a broken PRM.
		if !isJSONContentType(resp.Header.Get("Content-Type")) {
			return asRef{}, nil
		}
		return asRef{}, fmt.Errorf("decode protected resource metadata from %s: %w", p.MetadataURL, err)
	}
	if len(meta.AuthorizationServers) == 0 {
		return asRef{}, nil
	}
	if err := validatePRMResource(meta.Resource, p.ExpectedResource); err != nil {
		return asRef{}, fmt.Errorf("invalid protected resource metadata from %s: %w", p.MetadataURL, err)
	}
	return asRef{URL: meta.AuthorizationServers[0], Scopes: meta.ScopesSupported, Resource: meta.Resource}, nil
}

// Accept same-origin parent audiences for providers such as Slack, following the SDK's compatibility
// policy rather than RFC 9728's exact comparison:
// https://github.com/modelcontextprotocol/typescript-sdk/blob/3b73021a0a2272c0c04c3917bcdb03398b3ec63f/src/shared/auth-utils.ts#L25-L55
// https://www.rfc-editor.org/rfc/rfc9728.html#section-3.3
func validatePRMResource(resource, expected string) error {
	if resource == "" {
		return errors.New("resource is missing")
	}
	actual, err := resourceIdentity(resource)
	if err != nil {
		return fmt.Errorf("resource is invalid: %w", err)
	}
	server, err := resourceIdentity(expected)
	if err != nil {
		return fmt.Errorf("server resource is invalid: %w", err)
	}
	if actual.Scheme != server.Scheme || actual.Host != server.Host || !resourceContainsPath(actual, server) {
		return fmt.Errorf("resource %q does not contain server %q", resource, expected)
	}
	return nil
}

func resourceIdentity(raw string) (*url.URL, error) {
	if err := requireHTTPURL(raw); err != nil {
		return nil, err
	}
	canonical, err := CanonicalResourceURI(raw)
	if err != nil {
		return nil, err
	}
	return url.Parse(canonical)
}

func resourceContainsPath(resource, server *url.URL) bool {
	parent, child := resourcePath(resource), resourcePath(server)
	if len(child) < len(parent) {
		return false
	}
	return strings.HasPrefix(strings.TrimSuffix(child, "/")+"/", strings.TrimSuffix(parent, "/")+"/")
}

func resourcePath(u *url.URL) string {
	segments := strings.Split(u.EscapedPath(), "/")
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err == nil && (decoded == "." || decoded == "..") {
			segments[i] = decoded
		}
	}
	normalized := (&url.URL{Path: strings.Join(segments, "/")}).ResolveReference(&url.URL{})
	if normalized.Path == "" {
		return "/"
	}
	return normalized.Path
}

func isJSONContentType(ct string) bool {
	mediaType, _, err := mime.ParseMediaType(ct)
	return err == nil && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))
}
