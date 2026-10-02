package browserhttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/http2"
	"golang.org/x/net/idna"
	"golang.org/x/sync/singleflight"
)

const (
	protocolH2   = "h2"
	protocolHTTP = "http/1.1"

	// handoffTTL bounds how long a probed connection may wait for its
	// request. It is only reached when the probing caller gave up first.
	handoffTTL = 5 * time.Second
)

type chromeConfig struct {
	// rootCAs is nil for the system roots.
	rootCAs *x509.CertPool
	// proxy is nil for the environment.
	proxy func(*url.URL) (*url.URL, error)
}

type route int

const (
	routePlain route = iota
	routeChrome
)

// chooseRoute picks the transport family for u. Plain HTTP and HTTPS
// proxies cannot carry a uTLS handshake, so they use the standard library.
func chooseRoute(proxyFor func(*url.URL) (*url.URL, error), u *url.URL) (route, error) {
	if u.Scheme != "https" {
		return routePlain, nil
	}
	proxyURL, err := proxyFor(u)
	if err != nil {
		return routePlain, fmt.Errorf("resolving proxy for %s: %w", u.Host, err)
	}
	if proxyURL != nil && proxyURL.Scheme == "https" {
		return routePlain, nil
	}
	return routeChrome, nil
}

type handoffConn struct {
	conn *utls.UConn
	at   time.Time
}

// chromeTransport presents a Chrome ClientHello and then speaks whichever
// of HTTP/2 or HTTP/1.1 the server selected through ALPN.
//
// Invariant: a connection is only ever handed to the transport that matches
// its negotiated protocol.
type chromeTransport struct {
	dialer *chromeDialer
	plain  *http.Transport
	h1     *http.Transport
	h2     *http2.Transport

	// protocols maps host:port to the ALPN protocol seen on its last dial.
	protocols sync.Map
	// handoff holds the connection opened while probing a host, so the
	// first request reuses it instead of handshaking twice.
	handoff sync.Map
	probe   singleflight.Group
}

func newChromeTransport(cfg chromeConfig) *chromeTransport {
	proxyFor := cfg.proxy
	if proxyFor == nil {
		proxyFor = httpproxy.FromEnvironment().ProxyFunc()
	}

	t := &chromeTransport{dialer: &chromeDialer{proxy: proxyFor, rootCAs: cfg.rootCAs}}

	t.plain = newStandardTransport()
	if cfg.proxy != nil {
		t.plain.Proxy = func(r *http.Request) (*url.URL, error) { return cfg.proxy(r.URL) }
	}

	t.h1 = newStandardTransport()
	t.h1.Proxy = nil
	t.h1.ForceAttemptHTTP2 = false
	t.h1.DialTLSContext = t.dialForH1

	t.h2 = &http2.Transport{
		DialTLSContext:  t.dialForH2,
		IdleConnTimeout: idleConnTimeout,
	}
	return t
}

func (t *chromeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	chosen, err := chooseRoute(t.dialer.proxy, req.URL)
	if err != nil {
		return nil, err
	}
	if chosen == routePlain {
		return t.plain.RoundTrip(req)
	}

	proto, err := t.negotiatedProtocol(req.Context(), authority(req.URL))
	if err != nil {
		return nil, err
	}
	if proto == protocolH2 {
		return t.h2.RoundTrip(req)
	}
	return t.h1.RoundTrip(req)
}

func (t *chromeTransport) CloseIdleConnections() {
	t.plain.CloseIdleConnections()
	t.h1.CloseIdleConnections()
	t.h2.CloseIdleConnections()
	t.handoff.Range(func(addr, value any) bool {
		t.handoff.Delete(addr)
		_ = value.(handoffConn).conn.Close()
		return true
	})
}

func (t *chromeTransport) negotiatedProtocol(ctx context.Context, addr string) (string, error) {
	if proto, ok := t.protocols.Load(addr); ok {
		return proto.(string), nil
	}

	// NOTE: The probe outlives a canceled caller so concurrent callers
	// sharing it are not failed by one caller's cancellation.
	result := t.probe.DoChan(addr, func() (any, error) {
		return t.probeProtocol(context.WithoutCancel(ctx), addr)
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return "", r.Err
		}
		return r.Val.(string), nil
	}
}

func (t *chromeTransport) probeProtocol(ctx context.Context, addr string) (string, error) {
	if proto, ok := t.protocols.Load(addr); ok {
		return proto.(string), nil
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout+tlsHandshakeTimeout)
	defer cancel()

	conn, err := t.dialer.dialTLS(ctx, addr)
	if err != nil {
		return "", err
	}
	proto := protocolOf(conn)
	t.protocols.Store(addr, proto)
	if previous, loaded := t.handoff.Swap(addr, handoffConn{conn: conn, at: time.Now()}); loaded {
		_ = previous.(handoffConn).conn.Close()
	}
	return proto, nil
}

func (t *chromeTransport) dialForH1(ctx context.Context, _, addr string) (net.Conn, error) {
	return t.dialExpecting(ctx, addr, protocolHTTP)
}

func (t *chromeTransport) dialForH2(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
	return t.dialExpecting(ctx, addr, protocolH2)
}

func (t *chromeTransport) dialExpecting(ctx context.Context, addr, want string) (net.Conn, error) {
	addr = dialAddr(addr)
	conn, ok := t.takeHandoff(addr)
	if !ok {
		var err error
		conn, err = t.dialer.dialTLS(ctx, addr)
		if err != nil {
			return nil, err
		}
	}

	if got := protocolOf(conn); got != want {
		_ = conn.Close()
		// NOTE: Updating the cache lets the caller's next request pick the
		// right transport instead of failing the same way again.
		t.protocols.Store(addr, got)
		return nil, fmt.Errorf("%s negotiated %s but %s was expected", addr, got, want)
	}
	return conn, nil
}

func (t *chromeTransport) takeHandoff(addr string) (*utls.UConn, bool) {
	value, ok := t.handoff.LoadAndDelete(addr)
	if !ok {
		return nil, false
	}
	entry := value.(handoffConn)
	if time.Since(entry.at) > handoffTTL {
		_ = entry.conn.Close()
		return nil, false
	}
	return entry.conn, true
}

func protocolOf(conn *utls.UConn) string {
	if conn.ConnectionState().NegotiatedProtocol == protocolH2 {
		return protocolH2
	}
	return protocolHTTP
}

// authority returns the canonical host:port for u, the form every cache key
// and dial uses.
func authority(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(canonicalHost(u.Hostname()), port)
}

// dialAddr canonicalizes an address received by a dial function.
//
// NOTE: net/http keeps the casing of ASCII hosts and http2 skips IDNA
// mapping, so neither passes the form authority produces.
func dialAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort(canonicalHost(host), port)
}

// canonicalHost decodes any A-label first because http2 builds one from the
// original casing, which the Lookup profile rejects as a label but accepts
// once it is back in Unicode.
func canonicalHost(host string) string {
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	if unicodeHost, err := idna.Punycode.ToUnicode(host); err == nil {
		host = unicodeHost
	}
	if ascii, err := idna.Lookup.ToASCII(host); err == nil {
		return ascii
	}
	return strings.ToLower(host)
}
