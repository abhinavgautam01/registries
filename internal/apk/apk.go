// Package apk provides a registry client for Alpine APK repositories.
package apk

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/vers"
)

const (
	DefaultURL = ""
	ecosystem  = "apk"

	indexV2 = "APKINDEX.tar.gz"
	indexV3 = "Packages.adb"

	// maxIndexSize bounds the decompressed index archive. Alpine's
	// community index is about 10 MB uncompressed.
	maxIndexSize = 64 << 20
	maxLineSize  = 1 << 20

	archKey = "arch"
)

var (
	// ErrV3IndexUnsupported is returned when a repository publishes a v3
	// Packages.adb index, which this client does not decode yet.
	ErrV3IndexUnsupported = errors.New("apk v3 Packages.adb indexes are not supported")
	// ErrMalformedIndex is returned for indexes that cannot be parsed.
	ErrMalformedIndex = errors.New("malformed APKINDEX")
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

// Registry reads one repository architecture directory, such as
// https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64, or an exact index
// URL inside one. The release, component and architecture are never inferred
// from a higher-level mirror URL.
//
// The index is downloaded once per Registry and kept for its lifetime, so
// callers looking up many packages should reuse one Registry per repository.
type Registry struct {
	dirURL   string
	indexURL string // empty for a directory, which is probed
	client   *core.Client
	urls     *URLs

	mu  sync.Mutex
	idx *repoIndex
}

type repoIndex struct {
	packages map[string][]record
	// providers maps a virtual name from p:, such as so:libz.so.1, to the
	// package chosen to satisfy it.
	providers map[string]string
}

func New(baseURL string, client *core.Client) *Registry {
	if client == nil {
		client = core.DefaultClient()
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	r := &Registry{dirURL: baseURL, client: client}
	if strings.HasSuffix(baseURL, "/"+indexV2) || strings.HasSuffix(baseURL, "/"+indexV3) {
		r.indexURL = baseURL
		r.dirURL = baseURL[:strings.LastIndex(baseURL, "/")]
	}
	r.urls = &URLs{dirURL: r.dirURL}
	return r
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// record is one package entry from APKINDEX.
type record struct {
	name             string
	version          string
	arch             string
	description      string
	url              string
	license          string
	maintainer       string
	origin           string
	commit           string
	checksum         string
	buildTime        time.Time
	size             int64
	installedSize    int64
	providerPriority int64
	depends          []string
	provides         []string
	replaces         []string
	installIf        []string
}

// splitName strips a PURL vendor namespace: "alpine/curl" looks up "curl".
func splitName(name string) (namespace, pkg string) {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func (r *Registry) index(ctx context.Context) (*repoIndex, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idx != nil {
		return r.idx, nil
	}

	body, err := r.fetchIndex(ctx)
	if err != nil {
		return nil, err
	}
	records, err := parseIndexArchive(body)
	if err != nil {
		return nil, err
	}

	packages := make(map[string][]record)
	for _, rec := range records {
		packages[rec.name] = append(packages[rec.name], rec)
	}
	for _, recs := range packages {
		sort.SliceStable(recs, func(i, j int) bool {
			return vers.CompareWithScheme(recs[i].version, recs[j].version, ecosystem) > 0
		})
	}
	r.idx = &repoIndex{packages: packages, providers: buildProviders(records)}
	return r.idx, nil
}

// buildProviders picks one package per virtual name: the highest k: provider
// priority, then the lowest name so the choice is stable.
func buildProviders(records []record) map[string]string {
	type choice struct {
		name     string
		priority int64
	}
	best := make(map[string]choice)
	for _, rec := range records {
		for _, p := range rec.provides {
			virtual, _ := splitConstraint(p)
			cur, ok := best[virtual]
			if !ok || rec.providerPriority > cur.priority ||
				(rec.providerPriority == cur.priority && rec.name < cur.name) {
				best[virtual] = choice{rec.name, rec.providerPriority}
			}
		}
	}
	providers := make(map[string]string, len(best))
	for virtual, c := range best {
		providers[virtual] = c.name
	}
	return providers
}

// fetchIndex downloads APKINDEX.tar.gz. For a directory it asks for
// Packages.adb first, as apk-tools does, and falls back only when that is
// not found.
func (r *Registry) fetchIndex(ctx context.Context) ([]byte, error) {
	if strings.HasSuffix(r.indexURL, "/"+indexV3) {
		return nil, fmt.Errorf("%w: %s", ErrV3IndexUnsupported, r.indexURL)
	}
	if r.indexURL != "" {
		return r.client.GetBody(ctx, r.indexURL)
	}

	v3URL := r.dirURL + "/" + indexV3
	status, err := r.client.Head(ctx, v3URL)
	switch {
	case err != nil:
		return nil, err
	case status >= 200 && status < 300:
		return nil, fmt.Errorf("%w: %s", ErrV3IndexUnsupported, v3URL)
	case status != http.StatusNotFound:
		return nil, &core.HTTPError{StatusCode: status, URL: v3URL}
	}
	return r.client.GetBody(ctx, r.dirURL+"/"+indexV2)
}

// parseIndexArchive reads the APKINDEX member of an APKINDEX.tar.gz. The file
// is a signature gzip stream followed by the index stream, read as one tar.
func parseIndexArchive(data []byte) ([]record, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
	}
	defer func() { _ = gz.Close() }()

	limited := &io.LimitedReader{R: gz, N: maxIndexSize + 1}
	tr := tar.NewReader(limited)
	var index []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
		}
		if hdr.Name != "APKINDEX" || index != nil {
			continue
		}
		index, err = io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
		}
	}
	// tar EOF can precede gzip's checksum and size trailer.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("%w: index archive larger than %d bytes", ErrMalformedIndex, maxIndexSize)
	}
	if index == nil {
		return nil, fmt.Errorf("%w: no APKINDEX member", ErrMalformedIndex)
	}
	return parseIndex(index)
}

// parseIndex parses blank-line-separated records of single-letter fields,
// mapped as in apk-tools' apk_adb.c.
func parseIndex(data []byte) ([]record, error) {
	var records []record
	var rec record
	inRecord := false
	lineNo := 0

	finish := func() error {
		if !inRecord {
			return nil
		}
		if rec.name == "" || rec.version == "" {
			return fmt.Errorf("%w: record ending at line %d has no P or V field", ErrMalformedIndex, lineNo)
		}
		records = append(records, rec)
		rec = record{}
		inRecord = false
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, maxLineSize)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if line == "" {
			if err := finish(); err != nil {
				return nil, err
			}
			continue
		}
		if len(line) < 2 || line[1] != ':' {
			return nil, fmt.Errorf("%w: line %d is not a field", ErrMalformedIndex, lineNo)
		}
		inRecord = true
		if err := rec.set(line[0], line[2:]); err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", ErrMalformedIndex, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return records, nil
}

func (rec *record) set(key byte, value string) error {
	var err error
	switch key {
	case 'P':
		rec.name = value
	case 'V':
		rec.version = value
	case 'A':
		rec.arch = value
	case 'T':
		rec.description = value
	case 'U':
		rec.url = value
	case 'L':
		rec.license = value
	case 'm':
		rec.maintainer = value
	case 'o':
		rec.origin = value
	case 'c':
		rec.commit = value
	case 'C':
		rec.checksum = value
	case 'S':
		rec.size, err = strconv.ParseInt(value, 10, 64)
	case 'I':
		rec.installedSize, err = strconv.ParseInt(value, 10, 64)
	case 'k':
		rec.providerPriority, err = strconv.ParseInt(value, 10, 64)
	case 't':
		var secs int64
		if secs, err = strconv.ParseInt(value, 10, 64); err == nil {
			rec.buildTime = time.Unix(secs, 0).UTC()
		}
	case 'D':
		rec.depends = strings.Fields(value)
	case 'p':
		rec.provides = strings.Fields(value)
	case 'r':
		rec.replaces = strings.Fields(value)
	case 'i':
		rec.installIf = strings.Fields(value)
	}
	return err
}

// checksum is a decoded APKINDEX C: field. Q1 is the SHA-1 of the package's
// control section (its second gzip stream), not of the whole .apk file, so it
// is not usable as a download integrity value.
type checksum struct {
	Algorithm string
	Hex       string
}

// decodeChecksum decodes a C: value: "Q1" followed by base64 SHA-1, or a
// legacy hex MD5.
func decodeChecksum(value string) (checksum, bool) {
	if b64, ok := strings.CutPrefix(value, "Q1"); ok {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) != 20 {
			return checksum{}, false
		}
		return checksum{Algorithm: "sha1", Hex: hex.EncodeToString(raw)}, true
	}
	if raw, err := hex.DecodeString(value); err == nil && len(raw) == 16 {
		return checksum{Algorithm: "md5", Hex: value}, true
	}
	return checksum{}, false
}

// parseMaintainer parses "Name <email>"; any other value is kept whole as the name.
func parseMaintainer(value string) core.Maintainer {
	if open := strings.LastIndex(value, "<"); open >= 0 && strings.HasSuffix(value, ">") {
		name := strings.TrimSpace(value[:open])
		email := value[open+1 : len(value)-1]
		if email != "" && strings.Contains(email, "@") && !strings.ContainsAny(email, "<> ") {
			return core.Maintainer{Name: name, Email: email}
		}
	}
	return core.Maintainer{Name: strings.TrimSpace(value)}
}

func (r *Registry) lookup(ctx context.Context, name string) ([]record, error) {
	idx, err := r.index(ctx)
	if err != nil {
		return nil, err
	}
	_, pkg := splitName(name)
	recs := idx.packages[pkg]
	if len(recs) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	return recs, nil
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	namespace, _ := splitName(name)
	latest := recs[0]
	return &core.Package{
		Name:          latest.name,
		Description:   latest.description,
		Homepage:      latest.url,
		Licenses:      latest.license,
		Namespace:     namespace,
		LatestVersion: latest.version,
		Metadata: map[string]any{
			"origin": latest.origin,
			archKey:  latest.arch,
		},
	}, nil
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	versions := make([]core.Version, 0, len(recs))
	for _, rec := range recs {
		versions = append(versions, r.version(rec))
	}
	return versions, nil
}

func (r *Registry) version(rec record) core.Version {
	metadata := map[string]any{
		archKey:          rec.arch,
		"description":    rec.description,
		"url":            rec.url,
		"maintainer":     rec.maintainer,
		"origin":         rec.origin,
		"commit":         rec.commit,
		"size":           rec.size,
		"installed_size": rec.installedSize,
		"depends":        rec.depends,
		"provides":       rec.provides,
		"replaces":       rec.replaces,
		"install_if":     rec.installIf,
	}
	if rec.providerPriority != 0 {
		metadata["provider_priority"] = rec.providerPriority
	}
	if rec.checksum != "" {
		metadata["checksum"] = rec.checksum
		if sum, ok := decodeChecksum(rec.checksum); ok {
			metadata["checksum_algorithm"] = sum.Algorithm
			metadata["checksum_hex"] = sum.Hex
			if sum.Algorithm == "sha1" {
				metadata["checksum_covers"] = "control"
			}
		}
	}

	v := core.Version{
		Number:      rec.version,
		PublishedAt: rec.buildTime,
		Licenses:    rec.license,
		Metadata:    metadata,
	}
	if filename, ok := packageFilename(rec.name, rec.version); ok {
		v.Artifacts = []core.Artifact{{
			URL:      r.dirURL + "/" + filename,
			Filename: filename,
			Size:     rec.size,
			Metadata: map[string]any{archKey: rec.arch},
		}}
	}
	return v
}

// packageFilename refuses names and versions that would make the file URL
// point anywhere other than a file in the index's directory.
func packageFilename(name, version string) (string, bool) {
	if name == "" || version == "" || strings.ContainsAny(name+version, `/\?#%`) {
		return "", false
	}
	return name + "-" + version + ".apk", true
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if rec.version == version {
			return r.idx.dependencies(rec), nil
		}
	}
	return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
}

// dependencies resolves virtual names such as so:libz.so.1 to the package
// providing them. A constraint on a virtual name applies to the provided
// version, not the package's, so it is dropped. A virtual name with no
// provider in this index, usually one from another repository, is kept as is.
func (idx *repoIndex) dependencies(rec record) []core.Dependency {
	deps := make([]core.Dependency, 0, len(rec.depends))
	seen := map[string]int{rec.name: -1}
	for _, d := range rec.depends {
		if strings.HasPrefix(d, "!") {
			continue // conflict, kept in version metadata
		}
		depName, requirement := splitConstraint(d)
		if _, ok := idx.packages[depName]; !ok {
			if provider, ok := idx.providers[depName]; ok {
				depName, requirement = provider, ""
			}
		}
		// "python3 python3~3.12" is one dependency; ">=1 <2" stays as two.
		if i, ok := seen[depName]; ok {
			if i >= 0 && deps[i].Requirements == "" {
				deps[i].Requirements = requirement
			}
			if i < 0 || requirement == "" || deps[i].Requirements == requirement {
				continue
			}
		}
		seen[depName] = len(deps)
		deps = append(deps, core.Dependency{Name: depName, Requirements: requirement, Scope: core.Runtime})
	}
	return deps
}

// splitConstraint splits "libcurl=8.22.0-r0" into "libcurl" and "=8.22.0-r0".
func splitConstraint(dep string) (name, requirement string) {
	if i := strings.IndexAny(dep, "<>=~"); i > 0 {
		return dep[:i], dep[i:]
	}
	return dep, ""
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	recs, err := r.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if recs[0].maintainer == "" {
		return nil, nil
	}
	return []core.Maintainer{parseMaintainer(recs[0].maintainer)}, nil
}

type URLs struct {
	dirURL string
}

func (u *URLs) Registry(name, version string) string {
	return ""
}

// Download returns the package file beside the index. The name may carry a
// PURL vendor namespace.
func (u *URLs) Download(name, version string) string {
	_, pkg := splitName(name)
	filename, ok := packageFilename(pkg, version)
	if !ok {
		return ""
	}
	return u.dirURL + "/" + filename
}

func (u *URLs) Documentation(name, version string) string {
	return ""
}

func (u *URLs) PURL(name, version string) string {
	namespace, pkg := splitName(name)
	p := "pkg:apk/"
	if namespace != "" {
		p += namespace + "/"
	}
	p += pkg
	if version != "" {
		p += "@" + version
	}
	return p
}
