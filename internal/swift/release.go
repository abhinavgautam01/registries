package swift

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

type resource struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Checksum string         `json:"checksum"`
	Signing  map[string]any `json:"signing"`
}

type releaseInfo struct {
	ID          string         `json:"id"`
	Version     string         `json:"version"`
	Resources   []resource     `json:"resources"`
	Metadata    map[string]any `json:"metadata"`
	PublishedAt string         `json:"publishedAt"`
}

func (r *Registry) release(ctx context.Context, name, version, endpoint string) (*releaseInfo, error) {
	var info releaseInfo
	if _, err := r.getJSON(ctx, endpoint, name, version, &info); err != nil {
		return nil, err
	}
	if !strings.EqualFold(info.ID, name) || info.Version != version {
		return nil, fmt.Errorf("swift registry returned release %q@%q for %q@%q", info.ID, info.Version, name, version)
	}
	return &info, nil
}

func (info *releaseInfo) populatePackage(pkg *core.Package) {
	pkg.Description, _ = info.Metadata["description"].(string)
	repositories, _ := info.Metadata["repositoryURLs"].([]any)
	if pkg.Repository == "" {
		for _, repository := range repositories {
			value, _ := repository.(string)
			if normalized := urlparser.CanonicalURL(value); normalized != "" {
				pkg.Repository = normalized
				break
			}
		}
	}
}

func (info *releaseInfo) version(urls *URLs, name string) (*core.Version, error) {
	version := &core.Version{Number: info.Version, Metadata: info.Metadata}
	published := info.PublishedAt
	if original, _ := info.Metadata["originalPublicationTime"].(string); original != "" {
		published = original
	}
	if published != "" {
		var err error
		version.PublishedAt, err = time.Parse(time.RFC3339Nano, published)
		if err != nil {
			return nil, fmt.Errorf("invalid Swift release publication time: %w", err)
		}
	}
	for _, resource := range info.Resources {
		if resource.Name != "source-archive" || resource.Type != "application/zip" {
			continue
		}
		const sha256Bytes = 32
		checksum, err := hex.DecodeString(resource.Checksum)
		if err != nil || len(checksum) != sha256Bytes {
			return nil, fmt.Errorf("invalid Swift source archive SHA256 checksum")
		}
		if len(version.Artifacts) != 0 {
			return nil, fmt.Errorf("duplicate Swift source archive resource")
		}
		version.Integrity = "sha256-" + strings.ToLower(resource.Checksum)
		artifact := core.Artifact{
			URL: urls.Download(name, info.Version), Filename: info.Version + ".zip",
			Integrity: version.Integrity, MediaType: resource.Type,
		}
		if resource.Signing != nil {
			artifact.Metadata = map[string]any{"signing": resource.Signing}
		}
		version.Artifacts = append(version.Artifacts, artifact)
	}
	return version, nil
}
