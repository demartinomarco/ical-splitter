package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"marcodemartino.dev/ical-splitter/internal/icalsplit"
)

func TestHealthz(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	newHandler(&calendarCache{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
		t.Fatalf("response = %d %q, want 200 %q", recorder.Code, recorder.Body.String(), "ok\n")
	}
}

func TestCalendarEndpointNotReady(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/shared.ics", nil)
	newHandler(&calendarCache{}).ServeHTTP(recorder, request)
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
	request := httptest.NewRequest(http.MethodGet, "/shared.ics", nil)
	newHandler(cache).ServeHTTP(recorder, request)

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
