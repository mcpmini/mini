package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPrivateNetworkPolicyDestinations(t *testing.T) {
	policy := SSRFPolicy{AllowPrivateNetworks: true}
	for _, ip := range []string{"10.0.0.1", "127.0.0.1", "100.64.0.1", "fd00::1", "::1", "8.8.8.8", "64:ff9b::a00:1", "::ffff:127.0.0.1"} {
		if err := policy.ValidateURL("http://" + net.JoinHostPort(ip, "80")); err != nil {
			t.Errorf("allowed %s: %v", ip, err)
		}
	}
	for _, ip := range []string{"169.254.169.254", "0.0.0.0", "224.0.0.1", "240.0.0.1", "198.18.0.1", "fe80::1", "::", "ff02::1", "64:ff9b::a9fe:a9fe", "::ffff:169.254.169.254"} {
		t.Run(ip, func(t *testing.T) {
			if err := policy.ValidateURL("http://" + net.JoinHostPort(ip, "80")); err == nil {
				t.Fatal("forbidden literal accepted")
			}
			original := lookupSSRFHost
			t.Cleanup(func() { lookupSSRFHost = original })
			lookupSSRFHost = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP(ip)}}, nil
			}
			if _, err := policy.DialContext()(
				t.Context(),
				"tcp",
				"rebound.example:80",
			); err == nil ||
				!strings.Contains(err.Error(), "blocked") {
				t.Fatalf("mixed DNS not blocked: %v", err)
			}
		})
	}
}

func TestPrivateNetworkDialPinsValidatedDNS(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("validated")) }),
	)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	original := lookupSSRFHost
	t.Cleanup(func() { lookupSSRFHost = original })
	calls := 0
	lookupSSRFHost = func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		if calls > 1 {
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	tr := &http.Transport{DialContext: (SSRFPolicy{AllowPrivateNetworks: true}).DialContext()}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	resp, err := client.Get("http://rebound.example:" + u.Port())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "validated" || calls != 1 {
		t.Fatalf("body=%q err=%v DNS calls=%d", body, err, calls)
	}
}

type unguardedDefaultTransport struct{ calls int }

func (tr *unguardedDefaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls++
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestStrictClientGuardsNonstandardDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	unguarded := &unguardedDefaultTransport{}
	http.DefaultTransport = unguarded
	t.Cleanup(func() { http.DefaultTransport = original })
	client := NewNoRedirectClient(NoRedirectClientOptions{BlockPrivateIPs: true})
	defer client.CloseIdleConnections()
	response, err := client.Get("http://127.0.0.1:1")
	if response != nil {
		response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "blocked") || unguarded.calls != 0 {
		t.Fatalf("strict request used unguarded transport: %v calls=%d", err, unguarded.calls)
	}
}
