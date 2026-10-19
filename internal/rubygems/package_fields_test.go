package rubygems

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestFetchPackageFundingLinks(t *testing.T) {
	tests := []struct {
		name       string
		fundingURI string
		want       []string
	}{
		{"with funding uri", "https://github.com/sponsors/rails", []string{"https://github.com/sponsors/rails"}},
		{"without funding uri", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				resp := gemResponse{Name: "rails", Version: "7.1.0", FundingURI: tt.fundingURI}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			reg := New(server.URL, core.DefaultClient())
			pkg, err := reg.FetchPackage(context.Background(), "rails")
			if err != nil {
				t.Fatalf("FetchPackage failed: %v", err)
			}
			if !reflect.DeepEqual(pkg.FundingLinks, tt.want) {
				t.Errorf("FundingLinks = %#v, want %#v", pkg.FundingLinks, tt.want)
			}
			if pkg.Maintainers != nil {
				t.Errorf("Maintainers = %#v, want nil", pkg.Maintainers)
			}
		})
	}
}
