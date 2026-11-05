package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestBlockedAddresses(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.8.8.8", "::1",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fc00::1", "fd12::1",
		"169.254.169.254", "169.254.0.1", "fe80::1",
		"0.0.0.0", "::", "0.1.2.3",
		"100.64.0.1", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1",
		"224.0.0.1", "ff02::1", "240.0.0.1", "255.255.255.255",
		"2001:db8::1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1",
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe",
	}
	for _, raw := range blocked {
		if !IsBlocked(netip.MustParseAddr(raw)) {
			t.Errorf("%s is not blocked", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "142.250.1.1", "172.32.0.1", "2607:f8b0::1"} {
		if IsBlocked(netip.MustParseAddr(raw)) {
			t.Errorf("%s is blocked", raw)
		}
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addrs, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return addrs, nil
}

func newTestGuard(allowed ...string) *Guard {
	g := New(allowed)
	g.resolver = fakeResolver{
		"s3.example.com":   {netip.MustParseAddr("8.8.8.8")},
		"internal.example": {netip.MustParseAddr("10.0.0.5")},
		"mixed.example":    {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")},
		"nas.local":        {netip.MustParseAddr("192.168.1.20")},
	}
	return g
}

func TestCheckEndpointAccepts(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		raw     string
		want    string
	}{
		{"public https", nil, "https://s3.example.com", "https://s3.example.com"},
		{"trailing slash and case", nil, "HTTPS://S3.Example.com/", "https://s3.example.com"},
		{"public https with a port", nil, "https://s3.example.com:8443", "https://s3.example.com:8443"},
		{"an allowed private host", []string{"nas.local"}, "https://nas.local", "https://nas.local"},
		{"plain http on an allowed host", []string{"nas.local"}, "http://nas.local:9000", "http://nas.local:9000"},
		{"an allowed host and port", []string{"nas.local:9000"}, "http://nas.local:9000", "http://nas.local:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newTestGuard(tt.allowed...).CheckEndpoint(context.Background(), tt.raw)
			if err != nil {
				t.Fatalf("CheckEndpoint: %v", err)
			}
			if got != tt.want {
				t.Errorf("endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckEndpointRefuses(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		raw     string
	}{
		{"loopback address", nil, "https://127.0.0.1"},
		{"ipv6 loopback", nil, "https://[::1]"},
		{"cloud metadata", nil, "https://169.254.169.254"},
		{"a name resolving to a private address", nil, "https://internal.example"},
		{"a name with one private address among public ones", nil, "https://mixed.example"},
		{"plain http on a public host", nil, "http://s3.example.com"},
		{"another port on an allowed host", []string{"nas.local:9000"}, "http://nas.local:9001"},
		{"credentials in the url", nil, "https://user:pass@s3.example.com"},
		{"a path", nil, "https://s3.example.com/bucket"},
		{"a query", nil, "https://s3.example.com?x=1"},
		{"a fragment", nil, "https://s3.example.com#x"},
		{"another scheme", nil, "ftp://s3.example.com"},
		{"no scheme", nil, "s3.example.com"},
		{"no host", nil, "https://"},
		{"an unresolvable name", nil, "https://nowhere.example"},
		{"not a url", nil, "://"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestGuard(tt.allowed...).CheckEndpoint(context.Background(), tt.raw)
			if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("error = %v, want ErrNotAllowed", err)
			}
		})
	}
}

func listener(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

// The real protection: whatever a name resolved to earlier, the address actually
// being connected to is checked, which is what stops DNS rebinding.
func TestClientRefusesToConnectToBlockedAddresses(t *testing.T) {
	server := listener(t)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	client := New(nil).HTTPClient()
	for _, target := range []string{server.URL, "http://localhost:" + port} {
		resp, err := client.Get(target)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("GET %s succeeded, want refusal", target)
		}
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("GET %s error = %v, want ErrNotAllowed", target, err)
		}
	}
}

func TestClientConnectsToAllowedHosts(t *testing.T) {
	server := listener(t)
	host := strings.TrimPrefix(server.URL, "http://")
	resp, err := New([]string{host}).HTTPClient().Get(server.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	allowed := []string{strings.TrimPrefix(target.URL, "http://"), strings.TrimPrefix(redirector.URL, "http://")}
	resp, err := New(allowed).HTTPClient().Get(redirector.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if reached {
		t.Error("the redirect was followed")
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want the 302 itself", resp.StatusCode)
	}
}

func TestClientIgnoresProxySettings(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	transport, ok := New(nil).HTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport type")
	}
	if transport.Proxy != nil {
		t.Error("a proxy would connect on the client's behalf, skipping the address check")
	}
}
