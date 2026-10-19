package npm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackageMaintainersAndFunding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "demo",
			"name":      "demo",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{
					"name":    "demo",
					"version": "1.0.0",
					"funding": []interface{}{
						"https://github.com/sponsors/demo",
						map[string]string{"type": "opencollective", "url": "https://opencollective.com/demo"},
					},
				},
			},
			"maintainers": []map[string]string{
				{"name": "alice", "email": "alice@example.com"},
				{"name": "bob", "email": "bob@example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	wantMaintainers := []core.Maintainer{
		{UUID: "alice", Login: "alice", Email: "alice@example.com"},
		{UUID: "bob", Login: "bob", Email: "bob@example.com"},
	}
	if !reflect.DeepEqual(pkg.Maintainers, wantMaintainers) {
		t.Errorf("Maintainers = %#v, want %#v", pkg.Maintainers, wantMaintainers)
	}

	wantFunding := []string{"https://github.com/sponsors/demo", "https://opencollective.com/demo"}
	if !reflect.DeepEqual(pkg.FundingLinks, wantFunding) {
		t.Errorf("FundingLinks = %#v, want %#v", pkg.FundingLinks, wantFunding)
	}
}

func TestFetchPackageWithoutMaintainersOrFunding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"_id":       "bare",
			"name":      "bare",
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{"name": "bare", "version": "1.0.0"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "bare")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}
	if pkg.Maintainers != nil {
		t.Errorf("Maintainers = %#v, want nil", pkg.Maintainers)
	}
	if pkg.FundingLinks != nil {
		t.Errorf("FundingLinks = %#v, want nil", pkg.FundingLinks)
	}
}

func TestExtractFundingLinks(t *testing.T) {
	tests := []struct {
		name    string
		funding string
		want    []string
	}{
		{"string", `"https://github.com/sponsors/a"`, []string{"https://github.com/sponsors/a"}},
		{"object", `{"type":"patreon","url":"https://patreon.com/a"}`, []string{"https://patreon.com/a"}},
		{"object without url", `{"type":"patreon"}`, nil},
		{
			"array keeps order and drops duplicates",
			`["https://b.example",{"url":"https://a.example"},"https://b.example",""]`,
			[]string{"https://b.example", "https://a.example"},
		},
		{"null", `null`, nil},
		{"empty string", `""`, nil},
		{"unexpected type", `42`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v interface{}
			if err := json.Unmarshal([]byte(tt.funding), &v); err != nil {
				t.Fatalf("invalid test JSON: %v", err)
			}
			if got := extractFundingLinks(v); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractFundingLinks(%s) = %#v, want %#v", tt.funding, got, tt.want)
			}
		})
	}
}
