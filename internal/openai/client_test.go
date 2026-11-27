package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewEmptyKeyListModels(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env") // must not leak to the local server
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m","object":"model"}]}`))
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithVersion("v1"), WithApiKey(""), WithMaxRetries(0))
	if c == nil {
		t.Fatal("New returned nil for empty key")
	}
	page, err := c.ListModels(context.Background())
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != "m" {
		t.Fatalf("ListModels: %v %+v", err, page)
	}
	if auth != "" {
		t.Fatalf("Authorization sent with empty key: %q", auth)
	}

	c = New(WithBaseURL(srv.URL), WithVersion("v1"), WithApiKey("k"), WithMaxRetries(0))
	if _, err := c.ListModels(context.Background()); err != nil || !strings.EqualFold(auth, "Bearer k") {
		t.Fatalf("key not sent: %v %q", err, auth)
	}
}
