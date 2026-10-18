// Package clojars provides a registry client for clojars.org (Clojure).
package clojars

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	DefaultURL  = "https://clojars.org"
	ecosystem   = "clojars"
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

type artifactResponse struct {
	GroupName      string        `json:"group_name"`
	JarName        string        `json:"jar_name"`
	Description    string        `json:"description"`
	Homepage       string        `json:"homepage"`
	RecentVersions []versionInfo `json:"recent_versions"`
}

type versionInfo struct {
	Version   string `json:"version"`
	Downloads int    `json:"downloads"`
}

type versionDetailResponse struct {
	Version      string    `json:"version"`
	Description  string    `json:"description"`
	Homepage     string    `json:"homepage"`
	CreatedEpoch int64     `json:"created"`
	Licenses     []string  `json:"licenses"`
	SCM          scmInfo   `json:"scm"`
	Dependencies []depInfo `json:"dependencies"`
}

type scmInfo struct {
	URL        string `json:"url"`
	Tag        string `json:"tag"`
	Connection string `json:"connection"`
}

type depInfo struct {
	GroupName string `json:"group_name"`
	JarName   string `json:"jar_name"`
	Version   string `json:"version"`
	Scope     string `json:"scope"`
}

// ParseCoordinates parses a Clojars coordinate string (group/artifact or just artifact)
// If no group is specified, the artifact name is used as both group and artifact
func ParseCoordinates(name string) (group, artifact string) {
	if before, after, found := strings.Cut(name, "/"); found {
		return before, after
	}
	// Single name means group == artifact
	return name, name
}

func formatName(group, artifact string) string {
	if group == artifact {
		return artifact
	}
	return fmt.Sprintf("%s/%s", group, artifact)
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	group, artifact := ParseCoordinates(name)
	url := fmt.Sprintf("%s/api/artifacts/%s/%s", r.baseURL, group, artifact)

	var resp artifactResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	pkg := &core.Package{
		Name:        formatName(resp.GroupName, resp.JarName),
		Description: resp.Description,
		Homepage:    resp.Homepage,
		Namespace:   resp.GroupName,
		Metadata: map[string]any{
			"group_name": resp.GroupName,
			"jar_name":   resp.JarName,
		},
	}

	// Try to get more details from the latest version
	if len(resp.RecentVersions) > 0 {
		latestVersion := resp.RecentVersions[0].Version
		versionURL := fmt.Sprintf("%s/api/artifacts/%s/%s/versions/%s", r.baseURL, group, artifact, latestVersion)
		var versionResp versionDetailResponse
		if err := r.client.GetJSON(ctx, versionURL, &versionResp); err == nil {
			if versionResp.SCM.URL != "" {
				pkg.Repository = strings.TrimSuffix(versionResp.SCM.URL, ".git")
			}
			if len(versionResp.Licenses) > 0 {
				pkg.Licenses = strings.Join(versionResp.Licenses, ",")
			}
		}
	}

	return pkg, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	group, artifact := ParseCoordinates(name)
	url := fmt.Sprintf("%s/api/artifacts/%s/%s", r.baseURL, group, artifact)

	var resp artifactResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versions := make([]core.Version, len(resp.RecentVersions))
	for i, v := range resp.RecentVersions {
		versions[i] = core.Version{
			Number: v.Version,
			Metadata: map[string]any{
				"downloads": v.Downloads,
			},
		}

		// Try to get detailed version info
		versionURL := fmt.Sprintf("%s/api/artifacts/%s/%s/versions/%s", r.baseURL, group, artifact, v.Version)
		var versionResp versionDetailResponse
		if err := r.client.GetJSON(ctx, versionURL, &versionResp); err == nil {
			if versionResp.CreatedEpoch > 0 {
				versions[i].PublishedAt = time.Unix(versionResp.CreatedEpoch/msPerSecond, 0)
			}
			if len(versionResp.Licenses) > 0 {
				versions[i].Licenses = strings.Join(versionResp.Licenses, ",")
			}
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	group, artifact := ParseCoordinates(name)
	url := fmt.Sprintf("%s/api/artifacts/%s/%s/versions/%s", r.baseURL, group, artifact, version)

	var resp versionDetailResponse
	if err := r.client.GetJSON(ctx, url, &resp); err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	deps := make([]core.Dependency, len(resp.Dependencies))
	for i, d := range resp.Dependencies {
		depName := formatName(d.GroupName, d.JarName)
		scope := mapScope(d.Scope)

		deps[i] = core.Dependency{
			Name:         depName,
			Requirements: d.Version,
			Scope:        scope,
		}
	}

	return deps, nil
}

func mapScope(scope string) core.Scope {
	switch strings.ToLower(scope) {
	case "compile", "runtime", "":
		return core.Runtime
	case "test":
		return core.Test
	case "provided":
		return core.Build
	default:
		return core.Runtime
	}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Clojars API doesn't expose maintainers directly
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	group, artifact := ParseCoordinates(name)
	if group == artifact {
		if version != "" {
			return fmt.Sprintf("%s/%s/versions/%s", u.baseURL, artifact, version)
		}
		return fmt.Sprintf("%s/%s", u.baseURL, artifact)
	}
	if version != "" {
		return fmt.Sprintf("%s/%s/%s/versions/%s", u.baseURL, group, artifact, version)
	}
	return fmt.Sprintf("%s/%s/%s", u.baseURL, group, artifact)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	group, artifact := ParseCoordinates(name)
	groupPath := strings.ReplaceAll(group, ".", "/")
	return fmt.Sprintf("https://repo.clojars.org/%s/%s/%s/%s-%s.jar", groupPath, artifact, version, artifact, version)
}

func (u *URLs) Documentation(name, version string) string {
	group, artifact := ParseCoordinates(name)
	if group == artifact {
		return fmt.Sprintf("https://cljdoc.org/d/%s/CURRENT", artifact)
	}
	return fmt.Sprintf("https://cljdoc.org/d/%s/%s/CURRENT", group, artifact)
}

func (u *URLs) PURL(name, version string) string {
	group, artifact := ParseCoordinates(name)
	if version != "" {
		return fmt.Sprintf("pkg:clojars/%s/%s@%s", group, artifact, version)
	}
	return fmt.Sprintf("pkg:clojars/%s/%s", group, artifact)
}
