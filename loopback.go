package connectaip

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
)

// NewLoopbackTransport returns an http.RoundTripper that dispatches every
// request directly to the provided handler, bypassing the network. This
// lets in-process callers use a generated REST client (which speaks HTTP)
// to invoke the same mux that the external API handlers are registered on
// — no TCP, no port binding, no startup ordering gymnastics.
//
// Wrap it in &http.Client{Transport: ...} at the call site. Returning the
// transport (not a *http.Client) keeps the primitive composable with other
// RoundTripper middleware (auth, tracing, retry) and avoids inheriting
// default-Client behaviors like redirect following and a cookie jar that
// don't make sense for loopback traffic.
//
// Use this to wire REST clients to the api_server's own mux for
// service-to-service calls that used to be in-process ConnectRPC calls.
func NewLoopbackTransport(handler http.Handler) http.RoundTripper {
	return &loopbackTransport{handler: handler}
}

type loopbackTransport struct {
	handler http.Handler
}

func (t *loopbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	pr, pw := io.Pipe()
	rw := &loopbackResponseWriter{
		header: http.Header{},
		pw:     pw,
		ready:  make(chan struct{}),
	}
	done := make(chan struct{})

	go func() {
		// Defers run LIFO: signal done last so RoundTrip can wait on
		// the goroutine; close the pipe so the body reader sees EOF;
		// flush headers so RoundTrip unblocks if the handler returned
		// without writing; close req.Body; recover handler panics.
		// Observing EOF on the response body therefore implies req.Body
		// has already been closed.
		defer close(done)
		defer pw.Close()
		defer rw.flushHeaders()
		if req.Body != nil {
			defer req.Body.Close()
		}
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			slog.ErrorContext(ctx, "loopback handler panicked",
				"panic", p,
				"stack", string(debug.Stack()))
			if rw.headersSent() {
				// Headers are already in flight; we can't change the
				// status. Tear down the body so the caller sees a read
				// error instead of a silent truncation.
				_ = pw.CloseWithError(io.ErrClosedPipe)
				return
			}
			// Synthesize a 500 response so the caller gets a well-formed
			// HTTP error instead of an unrelated read error.
			rw.header.Set("Content-Type", "text/plain; charset=utf-8")
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(pw, "internal server error\n")
		}()
		t.handler.ServeHTTP(rw, req)
	}()

	// Wait for headers to be written (first Write/WriteHeader call, or
	// handler return). If the context is canceled first, tear down the
	// pipe so the handler goroutine unblocks on its next write, then
	// wait for it to finish so its cleanup (req.Body.Close, handler
	// deferred teardown) happens before we return.
	select {
	case <-rw.ready:
	case <-ctx.Done():
		cause := context.Cause(ctx)
		_ = pr.CloseWithError(cause)
		<-done
		return nil, cause
	}

	status := cmp.Or(rw.status, http.StatusOK)
	contentLength := int64(-1)
	if cl := rw.header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			contentLength = n
		}
	}
	return &http.Response{
		Status:        strconv.Itoa(status) + " " + http.StatusText(status),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rw.header,
		Body:          pr,
		ContentLength: contentLength,
		Request:       req,
	}, nil
}

type loopbackResponseWriter struct {
	header http.Header
	pw     *io.PipeWriter
	ready  chan struct{}
	once   sync.Once
	status int
}

func (w *loopbackResponseWriter) Header() http.Header {
	return w.header
}

func (w *loopbackResponseWriter) WriteHeader(status int) {
	w.once.Do(func() {
		w.status = status
		close(w.ready)
	})
}

func (w *loopbackResponseWriter) Write(b []byte) (int, error) {
	w.flushHeaders()
	return w.pw.Write(b)
}

func (w *loopbackResponseWriter) flushHeaders() {
	w.WriteHeader(http.StatusOK)
}

func (w *loopbackResponseWriter) headersSent() bool {
	select {
	case <-w.ready:
		return true
	default:
		return false
	}
}
