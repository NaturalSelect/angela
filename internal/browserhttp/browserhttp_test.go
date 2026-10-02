package browserhttp

import (
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/require"
)

func noProxy(*url.URL) (*url.URL, error) { return nil, nil }

func newTLSServer(t *testing.T, enableHTTP2 bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	srv.EnableHTTP2 = enableHTTP2
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &connections
}

func newTestTransport(t *testing.T, srv *httptest.Server, proxyFor func(*url.URL) (*url.URL, error)) *chromeTransport {
	t.Helper()

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	if proxyFor == nil {
		proxyFor = noProxy
	}

	transport := newChromeTransport(chromeConfig{rootCAs: roots, proxy: proxyFor})
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func get(t *testing.T, client *http.Client, target string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "hello", string(body))
	return resp.ProtoMajor
}

func getExpectingError(t *testing.T, client *http.Client, target string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
	return err
}

func TestChromeTransportSpeaksHTTP2WhenNegotiated(t *testing.T) {
	t.Parallel()

	srv, _ := newTLSServer(t, true)
	client := &http.Client{Transport: newTestTransport(t, srv, nil)}

	require.Equal(t, 2, get(t, client, srv.URL))
}

func TestChromeTransportFallsBackToHTTP1(t *testing.T) {
	t.Parallel()

	srv, _ := newTLSServer(t, false)
	client := &http.Client{Transport: newTestTransport(t, srv, nil)}

	require.Equal(t, 1, get(t, client, srv.URL))
}

func TestChromeTransportReusesProbeConnection(t *testing.T) {
	t.Parallel()

	for name, enableHTTP2 := range map[string]bool{"h2": true, "h1": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv, connections := newTLSServer(t, enableHTTP2)
			client := &http.Client{Transport: newTestTransport(t, srv, nil)}

			get(t, client, srv.URL)
			get(t, client, srv.URL)

			require.Equal(t, int32(1), connections.Load())
		})
	}
}

func TestChromeTransportSharesProbeBetweenConcurrentRequests(t *testing.T) {
	t.Parallel()

	srv, connections := newTLSServer(t, true)
	client := &http.Client{Transport: newTestTransport(t, srv, nil)}

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { get(t, client, srv.URL) })
	}
	wg.Wait()

	require.Equal(t, int32(1), connections.Load())
}

func TestChromeTransportSendsChromeClientHello(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	hellos := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		body := make([]byte, int(header[3])<<8|int(header[4]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		hellos <- append(header, body...)
	}()

	transport := newChromeTransport(chromeConfig{proxy: noProxy})
	t.Cleanup(transport.CloseIdleConnections)

	// NOTE: The handshake fails because the listener closes after reading
	// the ClientHello, which is all this test needs from it.
	err = getExpectingError(t, &http.Client{Transport: transport, Timeout: 5 * time.Second}, "https://"+listener.Addr().String())
	require.Error(t, err)

	var raw []byte
	select {
	case raw = <-hellos:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not receive a ClientHello")
	}

	spec, err := (&utls.Fingerprinter{AllowBluntMimicry: true}).FingerprintClientHello(raw)
	require.NoError(t, err)

	var greaseCount int
	var alpn []string
	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *utls.UtlsGREASEExtension:
			greaseCount++
		case *utls.ALPNExtension:
			alpn = e.AlpnProtocols
		}
	}
	// NOTE: crypto/tls never sends GREASE, so its presence separates this
	// handshake from the standard library one.
	require.NotZero(t, greaseCount, "expected GREASE extensions")
	require.Equal(t, []string{"h2", "http/1.1"}, alpn)
}

// tunnelTo answers a CONNECT request by splicing the client to
// upstreamAddr, whatever host the request named.
func tunnelTo(w http.ResponseWriter, r *http.Request, upstreamAddr string) {
	upstream, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", upstreamAddr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
}

func TestChromeTransportReusesProbeConnectionForMixedCaseHost(t *testing.T) {
	t.Parallel()

	for name, enableHTTP2 := range map[string]bool{"h2": true, "h1": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv, connections := newTLSServer(t, enableHTTP2)
			proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tunnelTo(w, r, srv.Listener.Addr().String())
			}))
			t.Cleanup(proxySrv.Close)

			proxyURL := mustParse(t, proxySrv.URL)
			transport := newTestTransport(t, srv, func(*url.URL) (*url.URL, error) { return proxyURL, nil })
			client := &http.Client{Transport: transport}

			// NOTE: The httptest certificate is valid for example.com, and the
			// proxy ignores the CONNECT host, so only the casing differs from
			// a plain request.
			_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
			require.NoError(t, err)
			target := "https://Example.COM:" + port + "/"

			get(t, client, target)
			get(t, client, target)

			require.Equal(t, int32(1), connections.Load())
			var stranded []any
			transport.handoff.Range(func(addr, _ any) bool {
				stranded = append(stranded, addr)
				return true
			})
			require.Empty(t, stranded)
		})
	}
}

func TestCanonicalHostAgreesAcrossDialerSpellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{"lowercase", "example.com", "example.com"},
		{"mixed case", "Example.COM", "example.com"},
		{"unicode", "BÜCHER.example", "xn--bcher-kva.example"},
		{"http1 punycode spelling", "xn--bcher-kva.example", "xn--bcher-kva.example"},
		{"http2 punycode spelling", "xn--BCHER-2pa.example", "xn--bcher-kva.example"},
		{"ipv4", "127.0.0.1", "127.0.0.1"},
		{"ipv6", "::1", "::1"},
		{"ipv6 zone keeps its case", "fe80::1%Eth0", "fe80::1%Eth0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, canonicalHost(tt.host))
		})
	}
}

func TestDialAddrMatchesAuthority(t *testing.T) {
	t.Parallel()

	require.Equal(t, authority(mustParse(t, "https://Example.COM/")), dialAddr("Example.COM:443"))
	require.Equal(t, "example.com:8443", dialAddr("EXAMPLE.com:8443"))
	require.Equal(t, "[::1]:443", dialAddr("[::1]:443"))
	require.Equal(t, "not-an-address", dialAddr("not-an-address"))
}

func TestChromeTransportTunnelsThroughHTTPProxy(t *testing.T) {
	t.Parallel()

	srv, _ := newTLSServer(t, true)

	var mu sync.Mutex
	var connectTarget, proxyAuth string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		connectTarget, proxyAuth = r.Host, r.Header.Get("Proxy-Authorization")
		mu.Unlock()

		tunnelTo(w, r, r.Host)
	}))
	t.Cleanup(proxySrv.Close)

	proxyURL, err := url.Parse(proxySrv.URL)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("user", "secret")

	client := &http.Client{Transport: newTestTransport(t, srv, func(*url.URL) (*url.URL, error) {
		return proxyURL, nil
	})}

	require.Equal(t, 2, get(t, client, srv.URL))

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, srvURL.Host, connectTarget)
	// base64("user:secret")
	require.Equal(t, "Basic dXNlcjpzZWNyZXQ=", proxyAuth)
}

func TestChromeTransportReportsRefusedTunnel(t *testing.T) {
	t.Parallel()

	srv, _ := newTLSServer(t, true)
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Cleanup(proxySrv.Close)

	proxyURL, err := url.Parse(proxySrv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: newTestTransport(t, srv, func(*url.URL) (*url.URL, error) {
		return proxyURL, nil
	})}

	err = getExpectingError(t, client, srv.URL)
	require.ErrorContains(t, err, "403")
}

func TestChromeTransportServesPlainHTTPThroughStandardTransport(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(srv.Close)

	transport := newChromeTransport(chromeConfig{proxy: noProxy})
	t.Cleanup(transport.CloseIdleConnections)

	get(t, &http.Client{Transport: transport}, srv.URL)
}

func TestChooseRoute(t *testing.T) {
	t.Parallel()

	httpsProxy := func(*url.URL) (*url.URL, error) { return url.Parse("https://proxy.example:8443") }
	httpProxy := func(*url.URL) (*url.URL, error) { return url.Parse("http://proxy.example:8080") }
	socksProxy := func(*url.URL) (*url.URL, error) { return url.Parse("socks5://proxy.example:1080") }

	tests := []struct {
		name     string
		proxyFor func(*url.URL) (*url.URL, error)
		target   string
		want     route
	}{
		{"plain http", noProxy, "http://example.com/", routePlain},
		{"https direct", noProxy, "https://example.com/", routeChrome},
		{"https via http proxy", httpProxy, "https://example.com/", routeChrome},
		{"https via socks5 proxy", socksProxy, "https://example.com/", routeChrome},
		{"https via https proxy", httpsProxy, "https://example.com/", routePlain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target, err := url.Parse(tt.target)
			require.NoError(t, err)
			got, err := chooseRoute(tt.proxyFor, target)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewClientStandardFingerprint(t *testing.T) {
	t.Parallel()

	client := NewClient(Options{Fingerprint: FingerprintGo, Timeout: 42 * time.Second})
	require.Equal(t, 42*time.Second, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "expected an *http.Transport")
	require.Equal(t, 100, transport.MaxIdleConns)
	require.Equal(t, 10, transport.MaxIdleConnsPerHost)
	require.Equal(t, 90*time.Second, transport.IdleConnTimeout)
}

func TestNewClientDefaultsToChrome(t *testing.T) {
	t.Parallel()

	client := NewClient(Options{})
	require.Equal(t, DefaultTimeout, client.Timeout)
	require.IsType(t, &chromeTransport{}, client.Transport)
}

func TestChromeTransportClosesIdleConnectionsThroughClient(t *testing.T) {
	t.Parallel()

	srv, _ := newTLSServer(t, false)
	client := &http.Client{Transport: newTestTransport(t, srv, nil)}
	get(t, client, srv.URL)

	client.CloseIdleConnections()

	_, stillHeld := client.Transport.(*chromeTransport).handoff.Load(authority(mustParse(t, srv.URL)))
	require.False(t, stillHeld)
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}
