package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
