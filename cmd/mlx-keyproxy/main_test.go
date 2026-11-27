package main

import (
	"errors"
	"flag"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	for addr, ok := range map[string]bool{"127.0.0.1:8099": true, "[::1]:8099": true, "0.0.0.0:8099": false, "192.168.1.5:80": false, "localhost:80": false, "127.0.0.1": false} {
		if got := requireLoopback(addr) == nil; got != ok {
			t.Errorf("requireLoopback(%q) ok = %v, want %v", addr, got, ok)
		}
	}
}

// runWith calls run() with the given command-line arguments. run() reads the global flag set and
// os.Args, so each call gets a fresh flag set, and the globals and the log output are restored after.
func runWith(t *testing.T, args ...string) error {
	t.Helper()
	oldFlags, oldArgs, oldLog := flag.CommandLine, os.Args, log.Writer()
	t.Cleanup(func() {
		flag.CommandLine, os.Args = oldFlags, oldArgs
		log.SetOutput(oldLog)
	})
	flag.CommandLine = flag.NewFlagSet("mlx-keyproxy", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = append([]string{"mlx-keyproxy"}, args...)
	log.SetOutput(io.Discard)
	return run()
}

func writeKeyFile(t *testing.T, name, contents string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun_RefusesBadSettings(t *testing.T) {
	good := writeKeyFile(t, "good", strings.Repeat("k", 16)+"\n")
	// 15 characters plus a newline is 16 bytes, so this only fails if the key is trimmed first.
	short := writeKeyFile(t, "short", strings.Repeat("k", 15)+"\n")
	missing := filepath.Join(t.TempDir(), "no-such-file")

	tests := []struct {
		name    string
		args    []string
		want    string // part of the error text
		wantIs  error  // or an error the result must wrap
		wantNot string // the key must never appear in the error
	}{
		{name: "upstream that does not parse", args: []string{"-upstream", "http://[::1", "-key-file", good}, want: "bad -upstream"},
		{name: "upstream without a host", args: []string{"-upstream", "not a url", "-key-file", good}, want: "bad -upstream"},
		{name: "listen address not loopback", args: []string{"-listen", "0.0.0.0:0", "-key-file", good}, want: "not a loopback IP address"},
		{name: "upstream not loopback", args: []string{"-upstream", "http://192.168.1.5:80", "-key-file", good}, want: "not a loopback IP address"},
		{name: "listen address without a port", args: []string{"-listen", "127.0.0.1", "-key-file", good}, want: "missing port"},
		{name: "upstream without a port", args: []string{"-upstream", "http://127.0.0.1", "-key-file", good}, want: "missing port"},
		{name: "key file missing", args: []string{"-key-file", missing}, wantIs: fs.ErrNotExist},
		{name: "key too short after trimming", args: []string{"-key-file", short}, want: "shorter than 16 characters", wantNot: strings.Repeat("k", 15)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runWith(t, tc.args...)
			if err == nil {
				t.Fatal("run() returned nil, want an error")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.wantIs)
			}
			if tc.wantNot != "" && strings.Contains(err.Error(), tc.wantNot) {
				t.Errorf("error %q leaks the key", err)
			}
		})
	}
}

// With valid settings run() goes on to listen. Here the port is already taken, so ListenAndServe
// fails at once instead of serving forever, and the listen error comes back from run().
func TestRun_ReturnsTheListenError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	good := writeKeyFile(t, "good", strings.Repeat("k", 16)+"\n")

	err = runWith(t, "-listen", taken.Addr().String(), "-key-file", good)
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Errorf("error = %v (%T), want a *net.OpError from listening on a taken port", err, err)
	}
}
