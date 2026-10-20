package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetJSONWithHeaders(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Header.Get("Accept") != "application/vnd.swift.registry.v1+json" || r.Header.Get("User-Agent") != "registries" {
			t.Errorf("headers = %#v", r.Header)
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Add("Link", `<?page=2>; rel="next"`)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	c := DefaultClient()
	c.MaxRetries = 1
	c.BaseDelay = 0
	requestHeaders := http.Header{"Accept": {"application/vnd.swift.registry.v1+json"}}
	var result struct{ OK bool }
	headers, err := c.GetJSONWithHeaders(context.Background(), server.URL, requestHeaders, &result)
	if err != nil || !result.OK || attempts != 2 || headers.Get("Link") == "" {
		t.Fatalf("result = %#v, headers = %#v, attempts = %d, err = %v", result, headers, attempts, err)
	}
	if requestHeaders.Get("User-Agent") != "" {
		t.Fatal("caller headers mutated")
	}
}

func TestGetJSONDefaultHeadersUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	var response map[string]any
	if err := DefaultClient().GetJSON(context.Background(), server.URL, &response); err != nil {
		t.Fatal(err)
	}
}

func TestGetJSONWithHeadersFailure(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Version", "1")
				if status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "10")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			c := DefaultClient()
			c.MaxRetries = 0
			var result any
			headers, err := c.GetJSONWithHeaders(context.Background(), server.URL, nil, &result)
			if err == nil || headers.Get("Content-Version") != "1" {
				t.Errorf("headers = %#v, err = %v", headers, err)
			}
		})
	}
}
