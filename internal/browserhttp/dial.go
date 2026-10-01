package browserhttp

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"
)

// NOTE: Pinned rather than HelloChrome_Auto so a uTLS upgrade cannot move
// the fingerprint away from ChromeUserAgent. Bump both together.
var chromeHello = utls.HelloChrome_133

const (
	dialTimeout         = 30 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
)

type chromeDialer struct {
	proxy   func(*url.URL) (*url.URL, error)
	rootCAs *x509.CertPool
}

func (d *chromeDialer) dialTLS(ctx context.Context, addr string) (*utls.UConn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	raw, err := d.dialRaw(ctx, addr)
	if err != nil {
		return nil, err
	}

	conn := utls.UClient(raw, &utls.Config{ServerName: host, RootCAs: d.rootCAs}, chromeHello)

	// NOTE: A custom dialer bypasses http.Transport.TLSHandshakeTimeout.
	handshakeCtx, cancel := context.WithTimeout(ctx, tlsHandshakeTimeout)
	defer cancel()
	if err := conn.HandshakeContext(handshakeCtx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("TLS handshake with %s failed: %w", addr, err)
	}
	return conn, nil
}

func (d *chromeDialer) dialRaw(ctx context.Context, addr string) (net.Conn, error) {
	proxyURL, err := d.proxy(&url.URL{Scheme: "https", Host: addr})
	if err != nil {
		return nil, fmt.Errorf("resolving proxy for %s: %w", addr, err)
	}

	base := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	if proxyURL == nil {
		return base.DialContext(ctx, "tcp", addr)
	}

	withPort := *proxyURL
	withPort.Host = proxyHostPort(proxyURL)

	switch proxyURL.Scheme {
	case "http":
		return dialHTTPConnect(ctx, base, &withPort, addr)
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(&withPort, base)
		if err != nil {
			return nil, fmt.Errorf("building SOCKS5 dialer: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5 dialer does not support contexts")
		}
		return contextDialer.DialContext(ctx, "tcp", addr)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

func proxyHostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if strings.HasPrefix(u.Scheme, "socks5") {
		port = "1080"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func dialHTTPConnect(ctx context.Context, base *net.Dialer, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	conn, err := base.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: targetAddr},
		Host:   targetAddr,
		Header: make(http.Header),
	}
	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	if err := establishTunnel(conn, req); err != nil {
		stop()
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if !stop() {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	return conn, nil
}

func establishTunnel(conn net.Conn, req *http.Request) error {
	if err := req.Write(conn); err != nil {
		return fmt.Errorf("writing CONNECT request: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return fmt.Errorf("reading CONNECT response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy refused CONNECT to %s: %s", req.Host, resp.Status)
	}
	// NOTE: The client speaks first inside the tunnel, so buffered bytes
	// here mean the proxy is misbehaving and would be silently dropped.
	if reader.Buffered() > 0 {
		return errors.New("proxy sent unexpected data after CONNECT response")
	}
	return nil
}
