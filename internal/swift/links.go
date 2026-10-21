package swift

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var linkPattern = regexp.MustCompile(`<([^>]*)>((?:[^",]|"(?:\\.|[^"\\])*")*)`)

func responseLinks(headers http.Header) map[string][]string {
	links := make(map[string][]string)
	for _, value := range headers.Values("Link") {
		for _, match := range linkPattern.FindAllStringSubmatch(value, -1) {
			_, params, err := mime.ParseMediaType("link" + strings.TrimSpace(match[2]))
			if err != nil {
				continue
			}
			for rel := range strings.FieldsSeq(strings.ToLower(params["rel"])) {
				links[rel] = append(links[rel], match[1])
			}
		}
	}
	return links
}

func resolveLink(baseURL, reference string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(ref)
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" ||
		resolved.User != nil || resolved.Fragment != "" {
		return "", fmt.Errorf("invalid Swift registry link %q", reference)
	}
	return resolved.String(), nil
}

func nextPage(page string, links map[string][]string) (string, error) {
	next := links["next"]
	if len(next) == 0 {
		return "", nil
	}
	if len(next) != 1 {
		return "", fmt.Errorf("multiple next-page links in Swift registry response")
	}
	resolved, err := resolveLink(page, next[0])
	if err != nil {
		return "", err
	}
	if !sameOrigin(page, resolved) {
		return "", fmt.Errorf("swift registry pagination changed origin")
	}
	return resolved, nil
}

func sameOrigin(a, b string) bool {
	first, err := url.Parse(a)
	if err != nil {
		return false
	}
	second, err := url.Parse(b)
	return err == nil && first.Scheme == second.Scheme && first.Host == second.Host
}
