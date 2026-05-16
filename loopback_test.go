package connectaip

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLoopbackClientDispatchesToHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Received-Method", r.Method)
		w.Header().Set("X-Received-Path", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"got":"` + string(body) + `"}`))
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, "http://loopback/v1/echo",
		strings.NewReader("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Received-Method"); got != http.MethodPost {
		t.Errorf("X-Received-Method = %q, want POST", got)
	}
	if got := resp.Header.Get("X-Received-Path"); got != "/v1/echo" {
		t.Errorf("X-Received-Path = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"got":"hello"}` {
		t.Errorf("body = %q", body)
	}
}

// Each call must get its own response body — two concurrent calls
// through the same loopback client must not share buffers.
func TestLoopbackClientBodyIsIndependentCopy(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/v1/count", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte("call-" + string(rune('0'+calls))))
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}

	resp1, err := client.Get("http://loopback/v1/count")
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := client.Get("http://loopback/v1/count")
	if err != nil {
		t.Fatal(err)
	}

	body1, _ := io.ReadAll(resp1.Body)
	body2, _ := io.ReadAll(resp2.Body)

	if string(body1) != "call-1" {
		t.Errorf("body1 = %q, want call-1", body1)
	}
	if string(body2) != "call-2" {
		t.Errorf("body2 = %q, want call-2", body2)
	}
}

// RoundTripper contract: req.Body is closed by RoundTrip. Normal
// http.Transport closes the body on return so callers with non-memory
// readers don't leak.
func TestLoopbackClientClosesRequestBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}

	body := &trackedReadCloser{data: []byte("hello")}
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, "http://loopback/v1/echo", body,
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Drain to EOF before checking — the handler goroutine closes
	// req.Body before closing the response pipe, so reaching EOF on
	// the response body proves req.Body has been closed.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if !body.closed {
		t.Error("request body was not closed by RoundTrip")
	}
}

type trackedReadCloser struct {
	data   []byte
	offset int
	closed bool
}

func (t *trackedReadCloser) Read(p []byte) (int, error) {
	if t.offset >= len(t.data) {
		return 0, io.EOF
	}
	n := copy(p, t.data[t.offset:])
	t.offset += n
	return n, nil
}

func (t *trackedReadCloser) Close() error {
	t.closed = true
	return nil
}

func TestLoopbackClientSurfacesHandlerErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/err", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}
	resp, err := client.Get("http://loopback/v1/err")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "boom") {
		t.Errorf("body %q does not contain 'boom'", body)
	}
}

// A handler panic before any response bytes are sent must be turned
// into a well-formed 500 response so the caller gets a normal HTTP
// error (and the goroutine must not crash the process).
func TestLoopbackClientPanicBeforeWriteReturns500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/panic", func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom")
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}

	resp, err := client.Get("http://loopback/v1/panic")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "internal server error") {
		t.Errorf("body %q missing error message", body)
	}
}

// Connect-RPC's server-streaming handlers type-assert the ResponseWriter
// to http.Flusher and error before sending the first frame if it's
// missing. The cheapest pin against future regressions.
func TestLoopbackResponseWriterImplementsFlusher(t *testing.T) {
	var w any = &loopbackResponseWriter{header: http.Header{}, ready: make(chan struct{})}
	if _, ok := w.(http.Flusher); !ok {
		t.Fatal("loopbackResponseWriter must implement http.Flusher")
	}
}

// End-to-end server-streaming through the loopback transport: handler
// flushes headers, then writes and flushes each frame. Reproducer for
// the Connect-RPC server-streaming bug — without Flush, the handler
// errored on its first frame.
func TestLoopbackTransportSupportsServerStreaming(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for _, line := range []string{"one\n", "two\n", "three\n"} {
			_, _ = io.WriteString(w, line)
			flusher.Flush()
		}
	})
	client := &http.Client{Transport: NewLoopbackTransport(handler)}
	resp, err := client.Get("http://loopback/stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(body); got != "one\ntwo\nthree\n" {
		t.Errorf("body = %q, want %q", got, "one\ntwo\nthree\n")
	}
}

// A handler panic after headers have already been sent cannot change
// the status. The caller observes a read error on the response body
// (and the goroutine itself must not crash).
func TestLoopbackClientPanicMidBodyClosesPipe(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/panic-mid", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("mid-body")
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}

	resp, err := client.Get("http://loopback/v1/panic-mid")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (panic happened after WriteHeader)", resp.StatusCode)
	}
	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("expected read error from panicking handler, got nil")
	}
}
