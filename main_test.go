package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"marcodemartino.dev/ical-splitter/internal/icalsplit"
)

const (
	testFirstSecret  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testSecondSecret = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	testSharedSecret = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestHealthz(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	newHandler(&calendarCache{}, testFirstSecret, testSecondSecret, testSharedSecret).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
		t.Fatalf("response = %d %q, want 200 %q", recorder.Code, recorder.Body.String(), "ok\n")
	}
}

func TestCalendarEndpointNotReady(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/"+testSharedSecret+".ics", nil)
	newHandler(&calendarCache{}, testFirstSecret, testSecondSecret, testSharedSecret).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestCalendarEndpointServesICS(t *testing.T) {
	cache := &calendarCache{}
	cache.store(icalsplit.Output{
		FirstOnly:  []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"),
		SecondOnly: []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"),
		Shared:     []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/"+testSharedSecret+".ics", nil)
	newHandler(cache, testFirstSecret, testSecondSecret, testSharedSecret).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("Content-Type = %q, want text/calendar", recorder.Header().Get("Content-Type"))
	}
	if !strings.Contains(recorder.Body.String(), "VERSION:2.0") {
		t.Fatalf("unexpected calendar body: %q", recorder.Body.String())
	}
}

func TestSecretCalendarEndpoints(t *testing.T) {
	cache := &calendarCache{}
	cache.store(icalsplit.Output{
		FirstOnly:  []byte("FIRST CALENDAR"),
		SecondOnly: []byte("SECOND CALENDAR"),
		Shared:     []byte("SHARED CALENDAR"),
	})
	handler := newHandler(cache, testFirstSecret, testSecondSecret, testSharedSecret)

	tests := []struct {
		path string
		want string
	}{
		{path: "/" + testFirstSecret + ".ics", want: "FIRST CALENDAR"},
		{path: "/" + testSecondSecret + ".ics", want: "SECOND CALENDAR"},
		{path: "/" + testSharedSecret + ".ics", want: "SHARED CALENDAR"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if recorder.Body.String() != test.want {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.want)
			}
		})
	}
}

func TestOldNamedEndpointsAreNotRegistered(t *testing.T) {
	cache := &calendarCache{}
	cache.store(icalsplit.Output{
		FirstOnly: []byte("FIRST"), SecondOnly: []byte("SECOND"), Shared: []byte("SHARED"),
	})
	handler := newHandler(cache, testFirstSecret, testSecondSecret, testSharedSecret)

	for _, path := range []string{"/first-only.ics", "/second-only.ics", "/shared.ics"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("path %q status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestValidateFeedURL(t *testing.T) {
	for _, valid := range []string{"https://example.test/feed.ics", "http://localhost:8000/feed.ics?token=abc"} {
		if err := validateFeedURL(valid); err != nil {
			t.Errorf("validateFeedURL(%q): %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "/feed.ics", "file:///tmp/feed.ics", "ftp://example.test/feed.ics"} {
		if err := validateFeedURL(invalid); err == nil {
			t.Errorf("validateFeedURL(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestValidateEndpointSecret(t *testing.T) {
	for _, valid := range []string{
		testFirstSecret,
		testSecondSecret,
		testSharedSecret,
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwx",
	} {
		if err := validateEndpointSecret(valid); err != nil {
			t.Errorf("validateEndpointSecret(%q): %v", valid, err)
		}
	}

	for _, invalid := range []string{
		"",
		"too-short",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd/extra",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abc?x",
		strings.Repeat("a", 129),
	} {
		if err := validateEndpointSecret(invalid); err == nil {
			t.Errorf("validateEndpointSecret(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestCalendarEndpointsRejectPost(t *testing.T) {
	cache := &calendarCache{}
	cache.store(icalsplit.Output{
		FirstOnly: []byte("FIRST"), SecondOnly: []byte("SECOND"), Shared: []byte("SHARED"),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/"+testFirstSecret+".ics", nil)
	newHandler(cache, testFirstSecret, testSecondSecret, testSharedSecret).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func icalWithEvent(uid string) string {
	return fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//ical-splitter main tests//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTART:20261027T140000Z\r\nDTEND:20261027T153000Z\r\nDTSTAMP:20261028T210000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", uid)
}

func applyEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
}

func validRunEnv(upstream string) map[string]string {
	return map[string]string{
		"FIRST_ICAL_URL":         upstream + "/first",
		"SECOND_ICAL_URL":        upstream + "/second",
		"FIRST_ENDPOINT_SECRET":  testFirstSecret,
		"SECOND_ENDPOINT_SECRET": testSecondSecret,
		"SHARED_ENDPOINT_SECRET": testSharedSecret,
		"REFRESH_INTERVAL":       "10m",
		"ADDR":                   "127.0.0.1:0",
	}
}

func TestRunWithInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		override map[string]string
		wantErr  string
	}{
		{
			name: "missing feed urls",
			override: map[string]string{
				"FIRST_ICAL_URL": "", "SECOND_ICAL_URL": "",
			},
			wantErr: "FIRST_ICAL_URL and SECOND_ICAL_URL must be configured",
		},
		{
			name:     "invalid first feed url",
			override: map[string]string{"FIRST_ICAL_URL": "not-a-url"},
			wantErr:  "FIRST_ICAL_URL",
		},
		{
			name:     "invalid second feed url",
			override: map[string]string{"SECOND_ICAL_URL": "ftp://example.test/feed.ics"},
			wantErr:  "SECOND_ICAL_URL",
		},
		{
			name:     "invalid first endpoint secret",
			override: map[string]string{"FIRST_ENDPOINT_SECRET": "too-short"},
			wantErr:  "FIRST_ENDPOINT_SECRET",
		},
		{
			name:     "invalid second endpoint secret",
			override: map[string]string{"SECOND_ENDPOINT_SECRET": "too-short"},
			wantErr:  "SECOND_ENDPOINT_SECRET",
		},
		{
			name:     "invalid shared endpoint secret",
			override: map[string]string{"SHARED_ENDPOINT_SECRET": "too-short"},
			wantErr:  "SHARED_ENDPOINT_SECRET",
		},
		{
			name: "duplicate endpoint secrets",
			override: map[string]string{
				"SECOND_ENDPOINT_SECRET": testFirstSecret,
				"SHARED_ENDPOINT_SECRET": testFirstSecret,
			},
			wantErr: "must all be different",
		},
		{
			name:     "invalid refresh interval",
			override: map[string]string{"REFRESH_INTERVAL": "not-a-duration"},
			wantErr:  "REFRESH_INTERVAL",
		},
		{
			name:     "non-positive refresh interval",
			override: map[string]string{"REFRESH_INTERVAL": "-1m"},
			wantErr:  "REFRESH_INTERVAL",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applyEnv(t, validRunEnv("https://example.test"))
			applyEnv(t, test.override)
			err := run()
			if err == nil {
				t.Fatal("run unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func pollEndpoint(t *testing.T, url string, wantStatus int, wantBody string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr == nil && response.StatusCode == wantStatus && strings.Contains(string(body), wantBody) {
			return
		}
		lastErr = fmt.Errorf("status %d body %q readErr %v", response.StatusCode, body, readErr)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("endpoint %s did not return status %d with body %q: %v", url, wantStatus, wantBody, lastErr)
}

func TestRunReturnsListenError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	applyEnv(t, validRunEnv("https://example.test"))
	applyEnv(t, map[string]string{"ADDR": listener.Addr().String()})
	if err := run(); err == nil {
		t.Fatal("run unexpectedly succeeded on an occupied address")
	}
}

func TestRunServesCalendarsUntilInterrupt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			_, _ = w.Write([]byte(icalWithEvent("first-run@example.test")))
		case "/second":
			_, _ = w.Write([]byte(icalWithEvent("second-run@example.test")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	addr := freeAddr(t)
	env := validRunEnv(upstream.URL)
	env["ADDR"] = addr
	env["REFRESH_INTERVAL"] = "1h"
	applyEnv(t, env)

	result := make(chan error, 1)
	go func() { result <- run() }()

	base := "http://" + addr
	pollEndpoint(t, base+"/healthz", http.StatusOK, "ok")
	pollEndpoint(t, base+"/readyz", http.StatusOK, "ready")

	response, err := http.Get(base + "/" + testFirstSecret + ".ics")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "first-run@example.test") {
		t.Fatalf("calendar endpoint status %d body %q", response.StatusCode, body)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after SIGINT")
	}
}

func TestCalendarCacheGetUnknownName(t *testing.T) {
	cache := &calendarCache{}
	cache.store(icalsplit.Output{FirstOnly: []byte("A"), SecondOnly: []byte("B"), Shared: []byte("C")})
	if _, ok := cache.get("not-a-calendar"); ok {
		t.Fatal("unknown calendar name unexpectedly found")
	}
	if !cache.ready {
		t.Fatal("store should mark the cache ready")
	}
}

func serveUpstream(firstBody, secondBody string, secondStatus int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			_, _ = w.Write([]byte(firstBody))
		case "/second":
			if secondStatus == http.StatusOK {
				_, _ = w.Write([]byte(secondBody))
				return
			}
			http.Error(w, "unavailable", secondStatus)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRefreshStoresOutput(t *testing.T) {
	server := serveUpstream(
		icalWithEvent("first-refresh@example.test"),
		icalWithEvent("second-refresh@example.test"),
		http.StatusOK)
	defer server.Close()
	cache := &calendarCache{}
	if err := cache.refresh(context.Background(), server.Client(), server.URL+"/first", server.URL+"/second"); err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}
	checks := map[string]string{
		"first-only":  "first-refresh@example.test",
		"second-only": "second-refresh@example.test",
	}
	for name, wantUID := range checks {
		body, ok := cache.get(name)
		if !ok {
			t.Fatalf("cache does not have %q", name)
		}
		if !strings.Contains(string(body), wantUID) {
			t.Errorf("%q body %q does not contain UID %q", name, body, wantUID)
		}
	}
}

func TestRefreshWrapsFetchError(t *testing.T) {
	server := serveUpstream(icalWithEvent("first@example.test"), "boom", http.StatusServiceUnavailable)
	defer server.Close()
	cache := &calendarCache{}
	err := cache.refresh(context.Background(), server.Client(), server.URL+"/first", server.URL+"/second")
	if err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "fetch second calendar") {
		t.Fatalf("error = %q, want it to mention the second calendar fetch", err)
	}
}

func TestRefreshWrapsFirstFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cache := &calendarCache{}
	err := cache.refresh(context.Background(), server.Client(), server.URL+"/first", server.URL+"/second")
	if err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "fetch first calendar") {
		t.Fatalf("error = %q, want it to mention the first calendar fetch", err)
	}
}

func TestRefreshWrapsSplitError(t *testing.T) {
	server := serveUpstream("BEGIN:VEVENT\nUID:x\nEND:VEVENT\n", "BEGIN:VEVENT\nUID:x\nEND:VEVENT\n", http.StatusOK)
	defer server.Close()
	cache := &calendarCache{}
	err := cache.refresh(context.Background(), server.Client(), server.URL+"/first", server.URL+"/second")
	if err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "compare calendars") {
		t.Fatalf("error = %q, want it to mention the comparison failure", err)
	}
}

func TestRefreshLoopStopsOnContextCancel(t *testing.T) {
	server := serveUpstream(
		icalWithEvent("first-loop@example.test"),
		icalWithEvent("second-loop@example.test"),
		http.StatusOK)
	defer server.Close()
	cache := &calendarCache{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		cache.refreshLoop(ctx, server.Client(), server.URL+"/first", server.URL+"/second", 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cache.get("shared"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := cache.get("shared"); !ok {
		t.Fatal("refresh loop did not populate the cache")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not stop after cancel")
	}
}

func TestRefreshLoopToleratesUpstreamFailures(t *testing.T) {
	server := serveUpstream("unavailable", "unavailable", http.StatusServiceUnavailable)
	defer server.Close()
	cache := &calendarCache{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.refreshLoop(ctx, server.Client(), server.URL+"/first", server.URL+"/second", 10*time.Millisecond)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond) // Let the initial refresh and at least one tick fail.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not stop after cancel")
	}
}

func TestFetchCalendarServesBody(t *testing.T) {
	want := []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	}))
	defer server.Close()
	got, err := fetchCalendar(context.Background(), server.Client(), server.URL+"/feed.ics")
	if err != nil {
		t.Fatalf("fetchCalendar returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestFetchCalendarRejectsBadResponses(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), maxFeedBytes+1)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "non-200 status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			wantErr: "upstream returned HTTP status 500",
		},
		{
			name: "truncated body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "10")
				_, _ = w.Write([]byte("short"))
			},
			wantErr: "could not read upstream response",
		},
		{
			name: "oversized body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(oversized)
			},
			wantErr: "upstream calendar exceeds",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			_, err := fetchCalendar(context.Background(), server.Client(), server.URL+"/feed.ics")
			if err == nil {
				t.Fatal("fetchCalendar unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestFetchCalendarRejectsInvalidAddress(t *testing.T) {
	if _, err := fetchCalendar(context.Background(), http.DefaultClient, "http://example.test/\x7f"); err == nil {
		t.Fatal("fetchCalendar unexpectedly accepted an invalid address")
	}
}

func TestFetchCalendarReportsUpstreamFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchCalendar(ctx, http.DefaultClient, "https://127.0.0.1:1/feed.ics")
	if err == nil {
		t.Fatal("fetchCalendar unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "upstream request failed") {
		t.Fatalf("error = %q, want it to mention the upstream request failure", err)
	}
}

func TestContextEndpointsRejectPost(t *testing.T) {
	handler := newHandler(&calendarCache{}, testFirstSecret, testSecondSecret, testSharedSecret)
	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %q status = %d, want %d", path, recorder.Code, http.StatusMethodNotAllowed)
		}
		if allow := recorder.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("POST %q Allow header = %q, want %q", path, allow, "GET, HEAD")
		}
	}
}

func TestReadyzEndpoint(t *testing.T) {
	cache := &calendarCache{}
	handler := newHandler(cache, testFirstSecret, testSecondSecret, testSharedSecret)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	cache.store(icalsplit.Output{FirstOnly: []byte("A"), SecondOnly: []byte("B"), Shared: []byte("C")})

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ready\n" {
		t.Fatalf("ready response = %d %q, want 200 %q", recorder.Code, recorder.Body.String(), "ready\n")
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/readyz", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "" {
		t.Fatalf("ready HEAD response = %d %q, want 200 with empty body", recorder.Code, recorder.Body.String())
	}
}

func TestEndpointsSupportHead(t *testing.T) {
	cache := &calendarCache{}
	cache.store(icalsplit.Output{FirstOnly: []byte("FIRST"), SecondOnly: []byte("SECOND"), Shared: []byte("SHARED")})
	handler := newHandler(cache, testFirstSecret, testSecondSecret, testSharedSecret)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/healthz", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "" {
		t.Fatalf("healthz HEAD response = %d %q, want 200 with empty body", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/"+testFirstSecret+".ics", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "" {
		t.Fatalf("calendar HEAD response = %d %q, want 200 with empty body", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "text/calendar; charset=utf-8" {
		t.Fatalf("calendar HEAD Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
}
