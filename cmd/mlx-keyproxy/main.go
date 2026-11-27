// Command mlx-keyproxy puts a Bearer-key check in front of mlx_lm.server, which has no auth of its own.
// Requests without "Authorization: Bearer <key>" get 401 and never reach the server.
package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

const minKeyLen = 16

func newHandler(key string, upstream *url.URL) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Header.Del("Authorization") // the server has no use for the key
		},
		FlushInterval: -1, // pass streamed (SSE) chunks through as they arrive
	}
	want := []byte(key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mlx"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// requireLoopback keeps both ends of the proxy off the network.
func requireLoopback(hostport string) error {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%q is not a loopback IP address", hostport)
	}
	return nil
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:8099", "address to listen on (loopback IP only)")
	upstreamURL := flag.String("upstream", "http://127.0.0.1:8098", "mlx_lm.server base URL (loopback IP only)")
	keyFile := flag.String("key-file", ".apiKey", "file holding the API key")
	flag.Parse()

	up, err := url.Parse(*upstreamURL)
	if err != nil || up.Host == "" {
		return fmt.Errorf("bad -upstream %q", *upstreamURL)
	}
	for _, addr := range []string{*listen, up.Host} {
		if err := requireLoopback(addr); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(string(raw))
	if len(key) < minKeyLen {
		return fmt.Errorf("key in %s is shorter than %d characters", *keyFile, minKeyLen)
	}

	srv := &http.Server{Addr: *listen, Handler: newHandler(key, up), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on %s, forwarding to %s (key read from %s, never logged)", *listen, up, *keyFile)
	return srv.ListenAndServe()
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
