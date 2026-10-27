package fetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/git-pkgs/registries"
	_ "github.com/git-pkgs/registries/internal/apk"
)

func TestResolveAPK(t *testing.T) {
	reg, err := registries.New("apk", "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	info, err := r.Resolve(context.Background(), "apk", "alpine/curl", "8.22.0-r0")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if want := "https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64/curl-8.22.0-r0.apk"; info.URL != want {
		t.Errorf("URL = %q, want %q", info.URL, want)
	}
	if info.Filename != "curl-8.22.0-r0.apk" {
		t.Errorf("Filename = %q", info.Filename)
	}
}

func TestResolveAPKArchQualifierMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/APKINDEX.tar.gz") {
			http.NotFound(w, r)
			return
		}
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		body := "P:curl\nV:8.22.0-r0\nA:x86_64\n"
		_ = tw.WriteHeader(&tar.Header{Name: "APKINDEX", Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
		_ = tw.Close()
		_ = gz.Close()
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	reg, err := registries.New("apk", server.URL+"/x86_64", registries.DefaultClient())
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	info, err := r.ResolveWithOptions(context.Background(), "apk", "alpine/curl", "8.22.0-r0",
		ResolveOptions{Qualifiers: map[string]string{"arch": "aarch64"}})
	if !errors.Is(err, ErrNoMatchingArtifact) {
		t.Fatalf("aarch64 against x86_64 = %+v, %v; want ErrNoMatchingArtifact", info, err)
	}
}

func TestResolveAPKRejectsPathInVersion(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	reg, err := registries.New("apk", server.URL+"/v3.23/main/x86_64", registries.DefaultClient())
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver()
	r.RegisterRegistry(reg)

	info, err := r.Resolve(context.Background(), "apk", "alpine/curl", "../../../edge/main/x86_64/other-1.0-r0")
	if err == nil {
		t.Fatalf("Resolve = %+v, want an error", info)
	}
	for _, p := range paths {
		if strings.Contains(p, "edge") || strings.HasSuffix(p, ".apk") {
			t.Errorf("requested %s", p)
		}
	}
}
