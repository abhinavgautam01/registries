// Package luarocks provides a registry client for Lua packages.
package luarocks

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://luarocks.org"
	ecosystem  = "luarocks"
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

type moduleResponse struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Homepage    string                   `json:"homepage"`
	License     string                   `json:"license"`
	Labels      []string                 `json:"labels"`
	Versions    map[string][]rockVersion `json:"versions"`
	Maintainers []maintainerInfo         `json:"maintainers"`
}

type rockVersion struct {
	RockfileURL string `json:"rockfile_url"`
	ArchiveURL  string `json:"arch_url"`
}

type maintainerInfo struct {
	Name string `json:"name"`
}

type rockspec struct {
	Package      string              `json:"package"`
	Version      string              `json:"version"`
	Description  rockspecDescription `json:"description"`
	Dependencies []string            `json:"dependencies"`
	Source       rockspecSource      `json:"source"`
}

type rockspecDescription struct {
	Summary             string `json:"summary"`
	DetailedDescription string `json:"detailed"`
	Homepage            string `json:"homepage"`
	License             string `json:"license"`
	Maintainer          string `json:"maintainer"`
}

type rockspecSource struct {
	URL string `json:"url"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/1/%s", r.baseURL, name)

	var resp moduleResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	return &core.Package{
		Name:        resp.Name,
		Description: resp.Description,
		Homepage:    resp.Homepage,
		Licenses:    resp.License,
		Keywords:    resp.Labels,
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/1/%s", r.baseURL, name)

	var resp moduleResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Extract version numbers
	versionNumbers := make([]string, 0, len(resp.Versions))
	for v := range resp.Versions {
		versionNumbers = append(versionNumbers, v)
	}

	// Sort versions (newest first by string comparison - not perfect for semver)
	sort.Sort(sort.Reverse(sort.StringSlice(versionNumbers)))

	versions := make([]core.Version, 0, len(versionNumbers))
	for _, v := range versionNumbers {
		versions = append(versions, core.Version{
			Number:   v,
			Licenses: resp.License,
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	// LuaRocks stores dependencies in the rockspec file
	// We need to fetch the manifest for the specific version
	url := fmt.Sprintf("%s/api/1/%s/%s", r.baseURL, name, version)

	var resp rockspec
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency
	for _, dep := range resp.Dependencies {
		depName, requirements := parseDependency(dep)
		if depName == "" || depName == "lua" {
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

// parseDependency parses a LuaRocks dependency string
// Format: "name version_constraint" or just "name"
// Examples: "lua >= 5.1", "lpeg", "luasocket >= 3.0"
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
	url := fmt.Sprintf("%s/api/1/%s", r.baseURL, name)

	var resp moduleResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	var maintainers []core.Maintainer
	for _, m := range resp.Maintainers {
		if m.Name != "" {
			maintainers = append(maintainers, core.Maintainer{
				Login: m.Name,
			})
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/modules/%s/%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/modules/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	// LuaRocks doesn't have a standardized download URL format
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	return u.Registry(name, version)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:luarocks/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:luarocks/%s", name)
}
