package hex

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
		resp := packageResponse{
			Name: "demo",
			Meta: metaInfo{
				Links: map[string]string{
					"GitHub":  "https://github.com/demo/demo",
					"Sponsor": "https://github.com/sponsors/demo",
				},
			},
			Owners: []ownerInfo{
				{Username: "alice", Email: "alice@example.com"},
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
	}
	if !reflect.DeepEqual(pkg.Maintainers, wantMaintainers) {
		t.Errorf("Maintainers = %#v, want %#v", pkg.Maintainers, wantMaintainers)
	}

	wantFunding := []string{"https://github.com/sponsors/demo"}
	if !reflect.DeepEqual(pkg.FundingLinks, wantFunding) {
		t.Errorf("FundingLinks = %#v, want %#v", pkg.FundingLinks, wantFunding)
	}
}

func TestFetchPackageWithoutMaintainersOrFunding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := packageResponse{
			Name: "bare",
			Meta: metaInfo{Links: map[string]string{"GitHub": "https://github.com/bare/bare"}},
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
