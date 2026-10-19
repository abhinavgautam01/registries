package pypi

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
		name        string
		projectURLs map[string]string
		want        []string
	}{
		{
			name: "funding and sponsor entries",
			projectURLs: map[string]string{
				"Source":  "https://github.com/pallets/flask",
				"Funding": "https://palletsprojects.com/donate",
				"Sponsor": "https://github.com/sponsors/pallets",
			},
			want: []string{"https://github.com/sponsors/pallets", "https://palletsprojects.com/donate"},
		},
		{
			name:        "no funding entries",
			projectURLs: map[string]string{"Source": "https://github.com/pallets/flask"},
			want:        nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				resp := versionInfoResponse{Info: infoBlock{Name: "flask", Version: "3.0.0", ProjectURLs: tt.projectURLs}}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			reg := New(server.URL, core.DefaultClient())
			pkg, err := reg.FetchPackage(context.Background(), "flask")
			if err != nil {
				t.Fatalf("FetchPackage failed: %v", err)
			}
			if !reflect.DeepEqual(pkg.FundingLinks, tt.want) {
				t.Errorf("FundingLinks = %#v, want %#v", pkg.FundingLinks, tt.want)
			}
		})
	}
}
