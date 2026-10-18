// Package elm provides a registry client for Elm packages.
package elm

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL  = "https://package.elm-lang.org"
	ecosystem   = "elm"
	msPerSecond = 1000
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

// parsePackageName splits "author/name" into author and name
func parsePackageName(name string) (author, pkg string) {
	parts := strings.SplitN(name, "/", 2) //nolint:mnd // author/name split
	if len(parts) == 2 {                  //nolint:mnd
		return parts[0], parts[1]
	}
	return "", name
}

type packageResponse struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	License string `json:"license"`
	Version string `json:"version"`
}

type elmJson struct {
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	Summary          string            `json:"summary"`
	License          string            `json:"license"`
	Version          string            `json:"version"`
	ExposedModules   interface{}       `json:"exposed-modules"`
	ElmVersion       string            `json:"elm-version"`
	Dependencies     map[string]string `json:"dependencies"`
	TestDependencies map[string]string `json:"test-dependencies"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	author, pkgName := parsePackageName(name)
	if author == "" {
		return nil, fmt.Errorf("elm package name must be in format 'author/name'")
	}

	// Get releases to find latest version
	releasesURL := fmt.Sprintf("%s/packages/%s/%s/releases.json", r.baseURL, author, pkgName)
	var releases map[string]int64
	if err := r.client.GetJSON(ctx, releasesURL, &releases); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Find latest version
	var latestVersion string
	var latestTime int64
	for v, t := range releases {
		if t > latestTime {
			latestTime = t
			latestVersion = v
		}
	}

	// Get elm.json for the latest version
	elmJsonURL := fmt.Sprintf("%s/packages/%s/%s/%s/elm.json", r.baseURL, author, pkgName, latestVersion)
	var elmInfo elmJson
	if err := r.client.GetJSON(ctx, elmJsonURL, &elmInfo); err != nil {
		return nil, err
	}

	return &core.Package{
		Name:        name,
		Description: elmInfo.Summary,
		Homepage:    fmt.Sprintf("https://package.elm-lang.org/packages/%s/%s/latest", author, pkgName),
		Repository:  urlparser.Parse(fmt.Sprintf("https://github.com/%s/%s", author, pkgName)),
		Licenses:    elmInfo.License,
		Namespace:   author,
		Metadata: map[string]any{
			"elm_version": elmInfo.ElmVersion,
			"type":        elmInfo.Type,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	author, pkgName := parsePackageName(name)
	if author == "" {
		return nil, fmt.Errorf("elm package name must be in format 'author/name'")
	}

	releasesURL := fmt.Sprintf("%s/packages/%s/%s/releases.json", r.baseURL, author, pkgName)
	var releases map[string]int64
	if err := r.client.GetJSON(ctx, releasesURL, &releases); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Convert to slice and sort by time (newest first)
	type versionTime struct {
		version string
		time    int64
	}
	vts := make([]versionTime, 0, len(releases))
	for v, t := range releases {
		vts = append(vts, versionTime{v, t})
	}
	sort.Slice(vts, func(i, j int) bool {
		return vts[i].time > vts[j].time
	})

	versions := make([]core.Version, 0, len(vts))
	for _, vt := range vts {
		versions = append(versions, core.Version{
			Number:      vt.version,
			PublishedAt: time.Unix(vt.time/msPerSecond, 0),
		})
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	author, pkgName := parsePackageName(name)
	if author == "" {
		return nil, fmt.Errorf("elm package name must be in format 'author/name'")
	}

	elmJsonURL := fmt.Sprintf("%s/packages/%s/%s/%s/elm.json", r.baseURL, author, pkgName, version)
	var elmInfo elmJson
	if err := r.client.GetJSON(ctx, elmJsonURL, &elmInfo); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	var deps []core.Dependency

	// Runtime dependencies
	for dep, constraint := range elmInfo.Dependencies {
		deps = append(deps, core.Dependency{
			Name:         dep,
			Requirements: constraint,
			Scope:        core.Runtime,
		})
	}

	// Test dependencies
	for dep, constraint := range elmInfo.TestDependencies {
		deps = append(deps, core.Dependency{
			Name:         dep,
			Requirements: constraint,
			Scope:        core.Test,
		})
	}

	// Sort for consistent output
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})

	return deps, nil
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Elm packages don't expose maintainer info via API
	// The author is derived from the package name
	author, _ := parsePackageName(name)
	if author == "" {
		return nil, nil
	}

	return []core.Maintainer{{
		Login: author,
		URL:   fmt.Sprintf("https://github.com/%s", author),
	}}, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	author, pkgName := parsePackageName(name)
	if author == "" {
		return ""
	}
	if version != "" {
		return fmt.Sprintf("%s/packages/%s/%s/%s", u.baseURL, author, pkgName, version)
	}
	return fmt.Sprintf("%s/packages/%s/%s/latest", u.baseURL, author, pkgName)
}

func (u *URLs) Download(name, version string) string {
	author, pkgName := parsePackageName(name)
	if author == "" || version == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/archive/%s.tar.gz", author, pkgName, version)
}

func (u *URLs) Documentation(name, version string) string {
	return u.Registry(name, version)
}

func (u *URLs) PURL(name, version string) string {
	author, pkgName := parsePackageName(name)
	if author == "" {
		return ""
	}
	if version != "" {
		return fmt.Sprintf("pkg:elm/%s/%s@%s", author, pkgName, version)
	}
	return fmt.Sprintf("pkg:elm/%s/%s", author, pkgName)
}
