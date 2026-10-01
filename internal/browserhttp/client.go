package browserhttp

import (
	"net/http"
	"time"
)

// Fingerprint selects the TLS ClientHello that clients present to servers.
type Fingerprint int

const (
	// FingerprintChrome is the zero value so that the default is the
	// browser-like handshake.
	FingerprintChrome Fingerprint = iota
	// FingerprintGo uses the standard library handshake.
	FingerprintGo
)

const (
	DefaultTimeout  = 30 * time.Second
	idleConnTimeout = 90 * time.Second
)

// NOTE: The major version must match chromeHello in dial.go.
const ChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

type Options struct {
	// Timeout is zero for DefaultTimeout.
	Timeout     time.Duration
	Fingerprint Fingerprint
}

func NewClient(opts Options) *http.Client {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	var transport http.RoundTripper
	switch opts.Fingerprint {
	case FingerprintGo:
		transport = newStandardTransport()
	default:
		transport = newChromeTransport(chromeConfig{})
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func newStandardTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = idleConnTimeout
	return transport
}
