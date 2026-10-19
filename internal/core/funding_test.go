package core

import (
	"reflect"
	"testing"
)

func TestIsFundingLabel(t *testing.T) {
	tests := []struct {
		label string
		want  bool
	}{
		{"Funding", true},
		{"Sponsor", true},
		{"GitHub Sponsors", true},
		{"Donate", true},
		{"donation", true},
		{"Open Collective Fund", true},
		{"Homepage", false},
		{"Source", false},
		{"Changelog", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsFundingLabel(tt.label); got != tt.want {
			t.Errorf("IsFundingLabel(%q) = %v, want %v", tt.label, got, tt.want)
		}
	}
}

func TestFundingLinksFromLabels(t *testing.T) {
	tests := []struct {
		name  string
		links map[string]string
		want  []string
	}{
		{
			name: "matches funding labels sorted and deduplicated",
			links: map[string]string{
				"Sponsor":  "https://github.com/sponsors/b",
				"Funding":  "https://opencollective.com/a",
				"Donate":   " https://github.com/sponsors/b ",
				"Homepage": "https://example.com",
			},
			want: []string{"https://github.com/sponsors/b", "https://opencollective.com/a"},
		},
		{
			name:  "skips empty values",
			links: map[string]string{"Funding": "  "},
			want:  nil,
		},
		{
			name:  "no funding labels",
			links: map[string]string{"Source": "https://github.com/a/b"},
			want:  nil,
		},
		{
			name:  "nil map",
			links: nil,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FundingLinksFromLabels(tt.links); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FundingLinksFromLabels() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAppendFundingLink(t *testing.T) {
	var links []string
	links = AppendFundingLink(links, "https://a.example")
	links = AppendFundingLink(links, "")
	links = AppendFundingLink(links, " https://a.example ")
	links = AppendFundingLink(links, "https://b.example")

	want := []string{"https://a.example", "https://b.example"}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("AppendFundingLink() = %#v, want %#v", links, want)
	}
	if got := AppendFundingLink(nil, ""); got != nil {
		t.Errorf("AppendFundingLink(nil, \"\") = %#v, want nil", got)
	}
}
