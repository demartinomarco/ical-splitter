package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"marcodemartino.dev/ical-splitter/internal/icalsplit"
)

const maxFeedBytes = 20 << 20 // 20 MiB per upstream feed.

// calendarCache serves the most recently generated calendars. Refreshes build
// a complete new result before swapping it in, so clients never see a partial
// refresh. If an upstream is temporarily unavailable, the last good result is
// kept.
type calendarCache struct {
	mu        sync.RWMutex
	output    icalsplit.Output
	ready     bool
	refreshed time.Time
}

func (c *calendarCache) store(output icalsplit.Output) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.output = output
	c.ready = true
	c.refreshed = time.Now().UTC()
}

func (c *calendarCache) get(name string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.ready {
		return nil, false
	}
	var body []byte
	switch name {
	case "first-only":
		body = c.output.FirstOnly
	case "second-only":
		body = c.output.SecondOnly
	case "shared":
		body = c.output.Shared
	default:
		return nil, false
	}
	// The cache is immutable after publication, but return a copy so callers
	// cannot accidentally mutate the shared byte slice.
	return append([]byte(nil), body...), true
}

func (c *calendarCache) refresh(ctx context.Context, client *http.Client, firstURL, secondURL string) error {
	first, err := fetchCalendar(ctx, client, firstURL)
	if err != nil {
		return fmt.Errorf("fetch first calendar: %w", err)
	}
	second, err := fetchCalendar(ctx, client, secondURL)
	if err != nil {
		return fmt.Errorf("fetch second calendar: %w", err)
	}
	output, err := icalsplit.Split(first, second)
	if err != nil {
		return fmt.Errorf("compare calendars: %w", err)
	}
	c.store(output)
	return nil
}

func fetchCalendar(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("invalid upstream request")
	}
	response, err := client.Do(request)
	if err != nil {
		// Do not include the URL in the error: subscription URLs often contain
		// private tokens in their path or query string.
		return nil, errors.New("upstream request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil {
		return nil, errors.New("could not read upstream response")
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("upstream calendar exceeds %d bytes", maxFeedBytes)
	}
	return body, nil
}

func (c *calendarCache) refreshLoop(ctx context.Context, client *http.Client, firstURL, secondURL string, interval time.Duration) {
	refresh := func() {
		if err := c.refresh(ctx, client, firstURL, secondURL); err != nil {
			log.Printf("calendar refresh failed: %v", err)
			return
		}
		log.Printf("calendar feeds refreshed successfully")
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

func newHandler(cache *calendarCache) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("ok\n"))
		}
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cache.mu.RLock()
		ready := cache.ready
		cache.mu.RUnlock()
		if !ready {
			http.Error(w, "calendar feeds not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("ready\n"))
		}
	})

	for path, name := range map[string]string{
		"/first-only.ics":  "first-only",
		"/second-only.ics": "second-only",
		"/shared.ics":      "shared",
	} {
		calendarName := name
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			body, ready := cache.get(calendarName)
			if !ready {
				http.Error(w, "calendar feeds not ready", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
		})
	}
	return mux
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	firstURL := strings.TrimSpace(os.Getenv("FIRST_ICAL_URL"))
	secondURL := strings.TrimSpace(os.Getenv("SECOND_ICAL_URL"))
	if firstURL == "" || secondURL == "" {
		return errors.New("FIRST_ICAL_URL and SECOND_ICAL_URL must be configured")
	}
	if err := validateFeedURL(firstURL); err != nil {
		return fmt.Errorf("FIRST_ICAL_URL: %w", err)
	}
	if err := validateFeedURL(secondURL); err != nil {
		return fmt.Errorf("SECOND_ICAL_URL: %w", err)
	}

	interval := 10 * time.Minute
	if value := strings.TrimSpace(os.Getenv("REFRESH_INTERVAL")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return errors.New("REFRESH_INTERVAL must be a positive Go duration, for example 10m")
		}
		interval = parsed
	}
	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		addr = ":8080"
	}

	client := &http.Client{Timeout: 20 * time.Second}
	cache := &calendarCache{}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go cache.refreshLoop(ctx, client, firstURL, secondURL, interval)

	server := &http.Server{
		Addr:              addr,
		Handler:           newHandler(cache),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown failed: %v", err)
		}
	}()

	log.Printf("listening on %s", addr)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateFeedURL(address string) error {
	parsed, err := url.ParseRequestURI(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("must be an absolute http:// or https:// URL")
	}
	return nil
}
