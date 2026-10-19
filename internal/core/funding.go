package core

import (
	"sort"
	"strings"
)

// fundingLabelWords are substrings that mark a registry link label as a
// funding page, matched case-insensitively ("Funding", "Sponsor", "Donate").
var fundingLabelWords = []string{"fund", "sponsor", "donat"}

// IsFundingLabel reports whether a link label from registry metadata names
// a funding page, such as PyPI project_urls or Hex package links.
func IsFundingLabel(label string) bool {
	label = strings.ToLower(label)
	for _, word := range fundingLabelWords {
		if strings.Contains(label, word) {
			return true
		}
	}
	return false
}

// FundingLinksFromLabels returns the URLs in links whose labels name a
// funding page. The result is sorted and deduplicated so output is stable
// across map iteration order. It returns nil when no labels match.
func FundingLinksFromLabels(links map[string]string) []string {
	var funding []string
	for label, link := range links {
		if IsFundingLabel(label) {
			funding = AppendFundingLink(funding, link)
		}
	}
	sort.Strings(funding)
	return funding
}

// AppendFundingLink appends link to links after trimming whitespace,
// skipping empty values and links already present.
func AppendFundingLink(links []string, link string) []string {
	link = strings.TrimSpace(link)
	if link == "" {
		return links
	}
	for _, existing := range links {
		if existing == link {
			return links
		}
	}
	return append(links, link)
}
