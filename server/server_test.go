package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type closer struct {
	name  string
	order *[]string
	mu    *sync.Mutex
	err   error
}

func (c closer) Close() error {
	c.mu.Lock()
	*c.order = append(*c.order, c.name)
	c.mu.Unlock()
	return c.err
}

type harness struct {
	t      *testing.T
	url    string
	sigs   chan os.Signal
	exited chan struct{}
	done   chan error
	cancel context.CancelFunc
}

func start(t *testing.T, c Config) *harness {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{
		t: t, url: "http://" + ln.Addr().String(), sigs: make(chan os.Signal, 2),
		exited: make(chan struct{}), done: make(chan error, 1), cancel: cancel,
	}
	var once sync.Once
	go func() { h.done <- serve(ctx, c, ln, h.sigs, func() { once.Do(func() { close(h.exited) }) }) }()
	t.Cleanup(cancel)
	return h
}

func (h *harness) get(path string) (int, map[string]any) {
	h.t.Helper()
	resp, err := http.Get(h.url + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func (h *harness) wait() error {
	h.t.Helper()
	select {
	case err := <-h.done:
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatal("serve did not return")
		return nil
	}
}

func appHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "app:"+r.Method+" "+r.URL.Path)
	})
}

func TestHealthEndpoints(t *testing.T) {
	Version = "v1.2.3"
	defer func() { Version = "" }()
	h := start(t, Config{
		Handler:    appHandler(),
		DrainDelay: -1,
		Health: Health{Timeout: 50 * time.Millisecond, Checks: map[string]Check{
			"db": func(context.Context) error { return nil },
		}},
	})

	if code, body := h.get("/healthz"); code != 200 || body["status"] != "ok" || body["version"] != "v1.2.3" {
		t.Errorf("healthz %d %v", code, body)
	}
	if code, body := h.get("/readyz"); code != 200 || body["status"] != "ok" ||
		body["checks"].(map[string]any)["db"] != "ok" {
		t.Errorf("readyz %d %v", code, body)
	}
	resp, err := http.Post(h.url+"/healthz", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "app:POST /healthz" {
		t.Errorf("non-GET health request not passed to app: %q", b)
	}
	resp, _ = http.Get(h.url + "/orders")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "app:GET /orders" {
		t.Errorf("app: %q", b)
	}
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatal(err)
	}
}

func TestReadyzFailures(t *testing.T) {
	h := start(t, Config{
		Handler:    appHandler(),
		DrainDelay: -1,
		Health: Health{Timeout: 50 * time.Millisecond, Checks: map[string]Check{
			"ok":     func(context.Context) error { return nil },
			"broken": func(context.Context) error { return errors.New("password=secret refused") },
			"slow": func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
			"stuck": func(context.Context) error { time.Sleep(time.Second); return nil },
		}},
	})
	start := time.Now()
	code, body := h.get("/readyz")
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("readyz waited %v for a stuck check", elapsed)
	}
	checks := body["checks"].(map[string]any)
	if code != 503 || body["status"] != "unavailable" || checks["ok"] != "ok" || checks["broken"] != "error" ||
		checks["slow"] != "timeout" || checks["stuck"] != "timeout" {
		t.Errorf("%d %v", code, body)
	}
	h.cancel()
	_ = h.wait()
}

func TestGracefulShutdownSequence(t *testing.T) {
	var mu sync.Mutex
	var order []string
	inFlight := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(inFlight)
			<-release
		}
		_, _ = io.WriteString(w, "done")
	})
	h := start(t, Config{
		Handler:    handler,
		DrainDelay: 200 * time.Millisecond,
		Closers: []io.Closer{
			closer{"db", &order, &mu, nil},
			closer{"cache", &order, &mu, errors.New("cache close failed")},
			nil,
			closer{"queue", &order, &mu, nil},
		},
	})

	slowResp := make(chan string, 1)
	go func() {
		resp, err := http.Get(h.url + "/slow")
		if err != nil {
			slowResp <- err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		slowResp <- string(b)
	}()
	<-inFlight

	h.sigs <- syscall.SIGTERM
	// During the drain delay /readyz flips to 503 while the app still serves.
	deadline := time.Now().Add(time.Second)
	for {
		code, body := h.get("/readyz")
		if code == 503 {
			if body["reason"] != "shutting down" {
				t.Errorf("%v", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readyz never became 503")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if got := <-slowResp; got != "done" {
		t.Errorf("in-flight request: %q", got)
	}

	err := h.wait()
	if err == nil || !strings.Contains(err.Error(), "cache close failed") {
		t.Errorf("err = %v", err)
	}
	if strings.Join(order, ",") != "queue,cache,db" {
		t.Errorf("close order %v", order)
	}
	select {
	case <-h.exited:
		t.Error("exit called on single signal")
	default:
	}
}

func TestSecondSignalForcesExit(t *testing.T) {
	h := start(t, Config{
		Handler:    http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		DrainDelay: time.Second,
	})
	h.sigs <- syscall.SIGINT
	time.Sleep(20 * time.Millisecond)
	h.sigs <- syscall.SIGINT
	select {
	case <-h.exited:
	case <-time.After(time.Second):
		t.Fatal("second signal did not force exit")
	}
}

func TestShutdownTimeout(t *testing.T) {
	inFlight := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	h := start(t, Config{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(inFlight)
			<-release
		}),
		DrainDelay:      -1,
		ShutdownTimeout: 50 * time.Millisecond,
	})
	go func() { _, _ = http.Get(h.url + "/") }()
	<-inFlight
	h.cancel()
	if err := h.wait(); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	var mu sync.Mutex
	var order []string
	err := Run(context.Background(), Config{Addr: "256.0.0.1:bad", Handler: appHandler(),
		Closers: []io.Closer{closer{"db", &order, &mu, nil}}})
	if err == nil || !strings.Contains(err.Error(), "listen") || len(order) != 1 {
		t.Errorf("err = %v, closed %v", err, order)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	err = serve(context.Background(), Config{}, ln, nil, func() {})
	if err == nil || !strings.Contains(err.Error(), "nil Handler") {
		t.Errorf("err = %v", err)
	}

	// Serve failing (listener closed underneath) is returned without a signal.
	ln, _ = net.Listen("tcp", "127.0.0.1:0")
	_ = ln.Close()
	err = serve(context.Background(), Config{Handler: appHandler()}, ln, nil, func() {})
	if err == nil {
		t.Error("want serve error")
	}
}

func TestRunWithContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Config{Addr: "127.0.0.1:0", Handler: appHandler(), DrainDelay: -1}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestBuildVersion(t *testing.T) {
	Version = ""
	if v := buildVersion(); v == "" {
		t.Error("empty version")
	}
	Version = "x"
	defer func() { Version = "" }()
	if buildVersion() != "x" {
		t.Error("ldflags version ignored")
	}
}
