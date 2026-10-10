package browserhttp

import (
	"net/http"
	"time"
)

const (
	DefaultTimeout  = 30 * time.Second
	idleConnTimeout = 90 * time.Second
)

// NOTE: The TLS handshake is Go's stock one, so sites that cross-check it
// against this User-Agent can still flag requests as bots.
const ChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

type Options struct {
	// Timeout is zero for DefaultTimeout.
	Timeout time.Duration
}

func NewClient(opts Options) *http.Client {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = idleConnTimeout
	return &http.Client{Timeout: timeout, Transport: transport}
}
