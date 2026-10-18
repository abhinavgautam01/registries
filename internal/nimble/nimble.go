// Package nimble provides a registry client for Nim packages.
package nimble

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://nimble.directory"
	ecosystem  = "nimble"
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

type packageResponse struct {
	Name        string        `json:"name"`
	URL         string        `json:"url"`
	Method      string        `json:"method"`
	Tags        []string      `json:"tags"`
	Description string        `json:"description"`
	License     string        `json:"license"`
	Web         string        `json:"web"`
	Versions    []versionInfo `json:"versions"`
}

type versionInfo struct {
	Version string `json:"version"`
}

type packageDetailResponse struct {
	Name        string          `json:"name"`
	Alias       string          `json:"alias"`
	URL         string          `json:"url"`
	Method      string          `json:"method"`
	Tags        []string        `json:"tags"`
	Description string          `json:"description"`
	License     string          `json:"license"`
	Web         string          `json:"web"`
	Doc         string          `json:"doc"`
	Versions    []versionDetail `json:"versions"`
}

type versionDetail struct {
	Version  string   `json:"version"`
	Requires []string `json:"requires"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageDetailResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Use web URL as homepage, or fall back to repository URL
	homepage := resp.Web
	if homepage == "" {
		homepage = resp.URL
	}

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Description,
		Homepage:    homepage,
		Repository:  urlparser.Parse(resp.URL),
		Licenses:    resp.License,
		Keywords:    resp.Tags,
		Metadata: map[string]any{
			"method": resp.Method,
			"doc":    resp.Doc,
			"alias":  resp.Alias,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageDetailResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		versions = append(versions, core.Version{
			Number:   v.Version,
			Licenses: resp.License,
		})
	}

	// Sort versions (newest first) - versions are typically already sorted by nimble
	// but we reverse them to get newest first
	for i, j := 0, len(versions)-1; i < j; i, j = i+1, j-1 {
		versions[i], versions[j] = versions[j], versions[i]
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/packages/%s", r.baseURL, name)

	var resp packageDetailResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	// Find the matching version
	var targetVersion *versionDetail
	for i := range resp.Versions {
		if resp.Versions[i].Version == version {
			targetVersion = &resp.Versions[i]
			break
		}
	}

	if targetVersion == nil {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}

	var deps []core.Dependency
	for _, req := range targetVersion.Requires {
		depName, requirements := parseDependency(req)
		if depName == "" || depName == "nim" {
			continue
		}

		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: requirements,
			Scope:        core.Runtime,
		})
	}

	// Sort for consistent output
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})

	return deps, nil
}

// parseDependency parses a Nimble dependency string
// Format: "name version_constraint" or just "name"
// Examples: "nim >= 1.0", "chronicles", "stew >= 0.1.0"
func parseDependency(dep string) (name, requirements string) {
	dep = strings.TrimSpace(dep)
	parts := strings.SplitN(dep, " ", 2) //nolint:mnd // name version split
	name = parts[0]
	if len(parts) > 1 {
		requirements = strings.TrimSpace(parts[1])
	}
	return
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Nimble directory doesn't expose maintainer info via API
	// The owner info is typically in the git repository
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/pkg/%s/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/pkg/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	// Nimble packages are typically installed from git repos
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	return fmt.Sprintf("%s/pkg/%s", u.baseURL, name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:nimble/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:nimble/%s", name)
}
