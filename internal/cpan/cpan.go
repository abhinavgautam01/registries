// Package cpan provides a registry client for CPAN/MetaCPAN (Perl).
package cpan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://fastapi.metacpan.org"
	ecosystem  = "cpan"
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	baseURL string
	client  *core.Client
	urls    *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
	}
	r.urls = &URLs{baseURL: r.baseURL}
	return r
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

type distributionResponse struct {
	Name      string   `json:"name"`
	Abstract  string   `json:"abstract"`
	Version   string   `json:"version"`
	License   []string `json:"license"`
	Author    string   `json:"author"`
	Resources struct {
		Homepage   string `json:"homepage"`
		Repository struct {
			URL  string `json:"url"`
			Web  string `json:"web"`
			Type string `json:"type"`
		} `json:"repository"`
		Bugtracker struct {
			Web string `json:"web"`
		} `json:"bugtracker"`
	} `json:"resources"`
	Dependency []dependencyInfo `json:"dependency"`
	Date       string           `json:"date"`
}

type dependencyInfo struct {
	Module       string `json:"module"`
	Version      string `json:"version"`
	Phase        string `json:"phase"`
	Relationship string `json:"relationship"`
}

type releaseSearchResponse struct {
	Hits struct {
		Hits []struct {
			Source releaseInfo `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type releaseInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Distribution string   `json:"distribution"`
	Date         string   `json:"date"`
	License      []string `json:"license"`
	Status       string   `json:"status"`
	Checksum     string   `json:"checksum_sha256"`
}

type authorResponse struct {
	Name    string   `json:"name"`
	Email   []string `json:"email"`
	PAUSEID string   `json:"pauseid"`
	Website []string `json:"website"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	// Normalize name: replace - with ::
	moduleName := strings.ReplaceAll(name, "-", "::")
	url := fmt.Sprintf("%s/v1/module/%s", r.baseURL, moduleName)

	var resp distributionResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var repository string
	if resp.Resources.Repository.Web != "" {
		repository = urlparser.Parse(resp.Resources.Repository.Web)
	}
	if repository == "" && resp.Resources.Repository.URL != "" {
		repository = urlparser.Parse(resp.Resources.Repository.URL)
	}

	var licenses string
	if len(resp.License) > 0 {
		licenses = strings.Join(resp.License, ",")
	}

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Abstract,
		Homepage:    resp.Resources.Homepage,
		Repository:  repository,
		Licenses:    licenses,
		Metadata: map[string]any{
			"author":     resp.Author,
			"bugtracker": resp.Resources.Bugtracker.Web,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	// Use the release endpoint to search for all versions
	distName := strings.ReplaceAll(name, "::", "-")
	url := fmt.Sprintf("%s/v1/release/_search?q=distribution:%s&size=100&sort=date:desc", r.baseURL, distName)

	var resp releaseSearchResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if len(resp.Hits.Hits) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}

	versions := make([]core.Version, len(resp.Hits.Hits))
	for i, hit := range resp.Hits.Hits {
		rel := hit.Source

		var publishedAt time.Time
		if rel.Date != "" {
			publishedAt, _ = time.Parse(time.RFC3339, rel.Date)
		}

		var status core.VersionStatus
		if rel.Status == "backpan" {
			status = core.StatusYanked
		}

		var integrity string
		if rel.Checksum != "" {
			integrity = "sha256-" + rel.Checksum
		}

		versions[i] = core.Version{
			Number:      rel.Version,
			PublishedAt: publishedAt,
			Licenses:    strings.Join(rel.License, ","),
			Status:      status,
			Integrity:   integrity,
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	// Fetch the release info
	distName := strings.ReplaceAll(name, "::", "-")
	releaseName := fmt.Sprintf("%s-%s", distName, version)
	url := fmt.Sprintf("%s/v1/release/%s", r.baseURL, releaseName)

	var resp distributionResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency
	for _, d := range resp.Dependency {
		// Skip perl itself
		if d.Module == "perl" {
			continue
		}

		scope := mapPhaseToScope(d.Phase, d.Relationship)
		optional := d.Relationship == "recommends" || d.Relationship == "suggests"

		deps = append(deps, core.Dependency{
			Name:         d.Module,
			Requirements: d.Version,
			Scope:        scope,
			Optional:     optional,
		})
	}

	return deps, nil
}

func mapPhaseToScope(phase, relationship string) core.Scope {
	if relationship == "recommends" || relationship == "suggests" {
		return core.Optional
	}

	switch phase {
	case "runtime":
		return core.Runtime
	case "test":
		return core.Test
	case "build", "configure":
		return core.Build
	case "develop":
		return core.Development
	default:
		return core.Runtime
	}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// First get the module to find the author
	moduleName := strings.ReplaceAll(name, "-", "::")
	moduleURL := fmt.Sprintf("%s/v1/module/%s", r.baseURL, moduleName)

	var moduleResp distributionResponse
	if err := r.client.GetJSON(ctx, moduleURL, &moduleResp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	if moduleResp.Author == "" {
		return nil, nil
	}

	// Fetch author details
	authorURL := fmt.Sprintf("%s/v1/author/%s", r.baseURL, moduleResp.Author)
	var authorResp authorResponse
	if err := r.client.GetJSON(ctx, authorURL, &authorResp); err != nil {
		// Return basic info if we can't get author details
		return []core.Maintainer{{Login: moduleResp.Author}}, nil
	}

	var email string
	if len(authorResp.Email) > 0 {
		email = authorResp.Email[0]
	}

	var website string
	if len(authorResp.Website) > 0 {
		website = authorResp.Website[0]
	}

	return []core.Maintainer{{
		UUID:  authorResp.PAUSEID,
		Login: authorResp.PAUSEID,
		Name:  authorResp.Name,
		Email: email,
		URL:   website,
	}}, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	distName := strings.ReplaceAll(name, "::", "-")
	if version != "" {
		return fmt.Sprintf("https://metacpan.org/release/%s/%s-%s", getAuthorPlaceholder(), distName, version)
	}
	return fmt.Sprintf("https://metacpan.org/dist/%s", distName)
}

func getAuthorPlaceholder() string {
	// Without making an API call, we can't know the author
	// Return a generic dist URL instead
	return ""
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	distName := strings.ReplaceAll(name, "::", "-")
	// CPAN download URLs require the author, which we don't have without an API call
	// Return a search URL that will redirect
	return fmt.Sprintf("https://cpan.metacpan.org/authors/id/%s-%s.tar.gz", distName, version)
}

func (u *URLs) Documentation(name, version string) string {
	moduleName := strings.ReplaceAll(name, "-", "::")
	if version != "" {
		return fmt.Sprintf("https://metacpan.org/pod/release/%s-%s/%s", strings.ReplaceAll(name, "::", "-"), version, moduleName)
	}
	return fmt.Sprintf("https://metacpan.org/pod/%s", moduleName)
}

func (u *URLs) PURL(name, version string) string {
	distName := strings.ReplaceAll(name, "::", "-")
	if version != "" {
		return fmt.Sprintf("pkg:cpan/%s@%s", distName, version)
	}
	return fmt.Sprintf("pkg:cpan/%s", distName)
}
