package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoggingHandler_NilLoggerPassesThrough verifies that a Server
// without a logger installed skips the request/response logging and
// simply delegates to the wrapped handler.
func TestLoggingHandler_NilLoggerPassesThrough(t *testing.T) {
	t.Parallel()

	s := &Server{}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	s.loggingHandler(next).ServeHTTP(rec, req)

	require.True(t, called, "the wrapped handler must still run")
	require.Equal(t, http.StatusTeapot, rec.Code)
}

// TestLoggingHandler_LogsRequestAndResponse verifies that a Server
// with a logger installed logs both the incoming request and the
// outgoing response, capturing the actual status code the handler
// wrote via the wrapping loggingResponseWriter.
func TestLoggingHandler_LogsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	s := &Server{logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces", nil)
	rec := httptest.NewRecorder()
	s.loggingHandler(next).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	logged := buf.String()
	require.Contains(t, logged, "HTTP request")
	require.Contains(t, logged, "HTTP response")
	require.Contains(t, logged, "status=201")
}

func TestLoggingResponseWriter_WriteHeader(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	lrw := &loggingResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	lrw.WriteHeader(http.StatusNotFound)

	require.Equal(t, http.StatusNotFound, lrw.statusCode)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLoggingResponseWriter_Unwrap(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	lrw := &loggingResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	require.Same(t, rec, lrw.Unwrap())
}
