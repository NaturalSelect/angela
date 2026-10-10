package browserhttp

import (
	"net/http"
	"time"
)

const (
	DefaultTimeout  = 30 * time.Second
	idleConnTimeout = 90 * time.Second
)

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
