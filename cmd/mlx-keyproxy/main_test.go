package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestKeyCheck(t *testing.T) {
	const key = "0123456789abcdef0123" // pragma: allowlist secret
	var hits int
	var upstreamAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		upstreamAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok")) // the test asserts status and hits only; the client may hang up first
	}))
	defer backend.Close()
	up, _ := url.Parse(backend.URL)
	proxy := httptest.NewServer(newHandler(key, up))
	defer proxy.Close()

	cases := []struct {
		name, header string
		want         int
		wantHits     int
	}{
		{"no header", "", 401, 0},
		{"wrong key", "Bearer nope", 401, 0},
		{"key without scheme", key, 401, 0},
		{"wrong scheme", "Basic " + key, 401, 0},
		{"right key", "Bearer " + key, 200, 1},
	}
	for _, c := range cases {
		hits = 0
		req, _ := http.NewRequest("POST", proxy.URL+"/v1/chat/completions", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close() // body is never read, so a close error cannot change what is asserted
		if resp.StatusCode != c.want || hits != c.wantHits {
			t.Errorf("%s: status %d (want %d), upstream hits %d (want %d)", c.name, resp.StatusCode, c.want, hits, c.wantHits)
		}
	}
	if upstreamAuth != "" {
		t.Errorf("key was forwarded upstream: %q", upstreamAuth)
	}
}

func TestRequireLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:8099": true, "[::1]:8099": true, "0.0.0.0:8099": false, "192.168.1.5:80": false, "localhost:80": false} {
		if got := requireLoopback(addr) == nil; got != ok {
			t.Errorf("requireLoopback(%q) ok = %v, want %v", addr, got, ok)
		}
	}
}
