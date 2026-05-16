package connectaip

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

// Streaming libraries (e.g. connect-sse) type-assert the ResponseWriter
// to http.Flusher to decide whether to push events incrementally. The
// loopback writer must advertise that capability.
func TestLoopbackResponseWriterImplementsFlusher(t *testing.T) {
	mux := http.NewServeMux()
	gotFlusher := make(chan bool, 1)
	mux.HandleFunc("/v1/flusher", func(w http.ResponseWriter, _ *http.Request) {
		_, ok := w.(http.Flusher)
		gotFlusher <- ok
		w.WriteHeader(http.StatusOK)
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}
	resp, err := client.Get("http://loopback/v1/flusher")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if ok := <-gotFlusher; !ok {
		t.Fatal("loopback ResponseWriter does not implement http.Flusher")
	}
}

// Flush before any Write must publish headers/status to the caller so
// the response is observable while the handler is still running — the
// handshake server-streaming handlers rely on.
func TestLoopbackFlushBeforeWriteUnblocksClient(t *testing.T) {
	mux := http.NewServeMux()
	handlerDone := make(chan struct{})
	release := make(chan struct{})
	mux.HandleFunc("/v1/early-flush", func(w http.ResponseWriter, _ *http.Request) {
		defer close(handlerDone)
		w.WriteHeader(http.StatusAccepted)
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("late"))
	})

	client := &http.Client{Transport: NewLoopbackTransport(mux)}

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://loopback/v1/early-flush")
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	var resp *http.Response
	select {
	case resp = <-respCh:
	case err := <-errCh:
		t.Fatalf("client.Get: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("client.Get did not return after handler Flush — Flusher contract broken")
	}
	defer resp.Body.Close()

	select {
	case <-handlerDone:
		t.Fatal("handler returned before client observed response; cannot prove Flush unblocked the caller mid-handler")
	default:
	}

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}

	close(release)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "late" {
		t.Errorf("body = %q, want %q", body, "late")
	}
	<-handlerDone
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
