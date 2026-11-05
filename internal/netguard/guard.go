// Package netguard stops user-supplied addresses from reaching internal networks.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	dialTimeout      = 10 * time.Second
	handshakeTimeout = 10 * time.Second
)

// ErrNotAllowed means an address is refused. It says nothing more on purpose, so
// the answer can't be used to map what's on the internal network.
var ErrNotAllowed = errors.New("address not allowed")

// blockedRanges are the ranges netip has no predicate for: shared, reserved and
// documentation space, and NAT64, which embeds an IPv4 address in an IPv6 one.
var blockedRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// IsBlocked reports whether addr is anything other than a public address.
func IsBlocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return true
	}
	for _, prefix := range blockedRanges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Guard decides which endpoints user-supplied connections may reach.
type Guard struct {
	allowed  map[string]bool
	resolver resolver
	client   *http.Client
}

// New returns a Guard. Each allowed entry, set by the operator, exempts a host or
// a host:port from the address checks.
func New(allowed []string) *Guard {
	g := &Guard{allowed: make(map[string]bool, len(allowed)), resolver: net.DefaultResolver}
	for _, entry := range allowed {
		g.allowed[strings.ToLower(entry)] = true
	}
	g.client = &http.Client{
		// No overall timeout, because transfers can legitimately take hours.
		Transport: &http.Transport{
			// Proxy stays nil: a proxy would make the connection on our behalf,
			// out of reach of the check in dial.
			DialContext:         g.dial,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: handshakeTimeout,
			IdleConnTimeout:     90 * time.Second,
		},
		// A redirect would take a checked request somewhere that wasn't checked.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return g
}

// HTTPClient returns a client that can only connect to permitted addresses.
func (g *Guard) HTTPClient() *http.Client {
	return g.client
}

func (g *Guard) isAllowed(host, port string) bool {
	host = strings.ToLower(host)
	return g.allowed[host] || g.allowed[net.JoinHostPort(host, port)]
}

// dial checks the address actually being connected to, after DNS resolution.
// That closes DNS rebinding, where a name resolves to a public address when
// checked and to an internal one when used.
func (g *Guard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: bad address", ErrNotAllowed)
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	if !g.isAllowed(host, port) {
		dialer.Control = refuseBlocked
	}
	return dialer.DialContext(ctx, network, address)
}

func refuseBlocked(_, address string, _ syscall.RawConn) error {
	parsed, err := netip.ParseAddrPort(address)
	if err != nil || IsBlocked(parsed.Addr().WithZone("")) {
		return ErrNotAllowed
	}
	return nil
}

// CheckEndpoint validates a user-supplied endpoint and returns it normalized. It
// exists to refuse bad input early with a clear answer; the check in dial is the
// one that can't be bypassed.
func (g *Guard) CheckEndpoint(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrNotAllowed
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", ErrNotAllowed
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", ErrNotAllowed
	}
	normalized := scheme + "://" + strings.ToLower(u.Host)

	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	if g.isAllowed(host, port) {
		return normalized, nil
	}
	if scheme != "https" {
		return "", ErrNotAllowed
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if IsBlocked(addr) {
			return "", ErrNotAllowed
		}
		return normalized, nil
	}
	addrs, err := g.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return "", ErrNotAllowed
	}
	if slices.ContainsFunc(addrs, IsBlocked) {
		return "", ErrNotAllowed
	}
	return normalized, nil
}
