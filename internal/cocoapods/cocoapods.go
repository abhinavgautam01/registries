// Package cocoapods provides a registry client for CocoaPods (iOS/macOS).
package cocoapods

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL = "https://trunk.cocoapods.org"
	ecosystem  = "cocoapods"
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

type podResponse struct {
	Name     string        `json:"name"`
	Versions []versionInfo `json:"versions"`
	Owners   []ownerInfo   `json:"owners"`
}

type versionInfo struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Spec      podSpec   `json:"spec"`
}

type podSpec struct {
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	Summary       string                 `json:"summary"`
	Description   string                 `json:"description"`
	Homepage      string                 `json:"homepage"`
	License       interface{}            `json:"license"`
	Authors       interface{}            `json:"authors"`
	Source        map[string]interface{} `json:"source"`
	Dependencies  map[string]interface{} `json:"dependencies"`
	Platforms     map[string]string      `json:"platforms"`
	SwiftVersions interface{}            `json:"swift_versions"`
}

type ownerInfo struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	url := fmt.Sprintf("%s/api/v1/pods/%s", r.baseURL, url.PathEscape(name))

	var resp podResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Get the latest version's spec
	var latestSpec *podSpec
	for i := len(resp.Versions) - 1; i >= 0; i-- {
		if resp.Versions[i].Spec.Name != "" {
			latestSpec = &resp.Versions[i].Spec
			break
		}
	}

	if latestSpec == nil && len(resp.Versions) > 0 {
		latestSpec = &resp.Versions[len(resp.Versions)-1].Spec
	}

	pkg := &core.Package{
		Name: resp.Name,
	}

	if latestSpec != nil {
		pkg.Description = latestSpec.Summary
		if pkg.Description == "" {
			pkg.Description = latestSpec.Description
		}
		pkg.Homepage = latestSpec.Homepage
		pkg.Repository = core.ExtractRepoURL(latestSpec.Source)
		pkg.Licenses = core.ExtractLicense(latestSpec.License)
	}

	return pkg, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	url := fmt.Sprintf("%s/api/v1/pods/%s", r.baseURL, url.PathEscape(name))

	var resp podResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, len(resp.Versions))
	for i, v := range resp.Versions {
		versions[i] = core.Version{
			Number:      v.Name,
			PublishedAt: v.CreatedAt,
			Licenses:    core.ExtractLicense(v.Spec.License),
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	url := fmt.Sprintf("%s/api/v1/pods/%s", r.baseURL, url.PathEscape(name))

	var resp podResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	// Find the specific version
	var spec *podSpec
	for _, v := range resp.Versions {
		if v.Name == version {
			spec = &v.Spec
			break
		}
	}

	if spec == nil {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
	}

	var deps []core.Dependency
	for depName, req := range spec.Dependencies {
		deps = append(deps, core.Dependency{
			Name:         depName,
			Requirements: formatRequirement(req),
			Scope:        core.Runtime,
		})
	}

	return deps, nil
}

func formatRequirement(req interface{}) string {
	switch v := req.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, len(v))
		for i, p := range v {
			if s, ok := p.(string); ok {
				parts[i] = s
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	url := fmt.Sprintf("%s/api/v1/pods/%s", r.baseURL, url.PathEscape(name))

	var resp podResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	maintainers := make([]core.Maintainer, len(resp.Owners))
	for i, owner := range resp.Owners {
		maintainers[i] = core.Maintainer{
			Name:  owner.Name,
			Email: owner.Email,
		}
	}

	return maintainers, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://cocoapods.org/pods/%s", name)
	}
	return fmt.Sprintf("https://cocoapods.org/pods/%s", name)
}

func (u *URLs) Download(name, version string) string {
	// CocoaPods doesn't have direct download URLs; packages are fetched via git/http sources
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("https://cocoadocs.org/docsets/%s/%s/", name, version)
	}
	return fmt.Sprintf("https://cocoadocs.org/docsets/%s/", name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:cocoapods/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:cocoapods/%s", name)
}
