package gameversions

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMinecraftURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"
	defaultNeoForgeURL  = "https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml"
	maxMetadataBytes    = 8 << 20
	maxCatalogEntries   = 5000
	fetchTimeout        = 20 * time.Second
)

var neoForgeVersionDirectory = regexp.MustCompile(`href="\./([0-9]+(?:\.[0-9]+)+)/"`)

// Catalog is the official release list consumed by the administrator's
// searchable version pickers. NeoForge versions are ordered newest first.
type Catalog struct {
	Minecraft []string  `json:"minecraft"`
	NeoForge  []string  `json:"neoforge"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Provider fetches official metadata and keeps a short in-memory cache so
// opening the profile form does not contact upstream services on every visit.
type Provider struct {
	client       *http.Client
	minecraftURL string
	neoForgeURL  string
	ttl          time.Duration

	mu      sync.Mutex
	cached  Catalog
	expires time.Time
}

func NewProvider(client *http.Client, minecraftURL, neoForgeURL string, ttl time.Duration) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if minecraftURL == "" {
		minecraftURL = defaultMinecraftURL
	}
	if neoForgeURL == "" {
		neoForgeURL = defaultNeoForgeURL
	}
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	return &Provider{client: client, minecraftURL: minecraftURL, neoForgeURL: neoForgeURL, ttl: ttl}
}

func (p *Provider) Get(ctx context.Context) (Catalog, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.cached.Minecraft) > 0 && len(p.cached.NeoForge) > 0 {
		if time.Now().Before(p.expires) {
			return clone(p.cached), nil
		}
		// Keep serving the last complete catalog through a temporary upstream
		// outage, but refresh it again on the next request.
		stale := clone(p.cached)
		fresh, err := p.fetch(ctx)
		if err != nil {
			p.expires = time.Now().Add(time.Minute)
			return stale, nil
		}
		p.cached = fresh
		p.expires = time.Now().Add(p.ttl)
		return clone(fresh), nil
	}
	fresh, err := p.fetch(ctx)
	if err != nil {
		return Catalog{}, err
	}
	p.cached = fresh
	p.expires = time.Now().Add(p.ttl)
	return clone(fresh), nil
}

func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	catalog, err := p.Get(r.Context())
	if err != nil {
		http.Error(w, "official game version catalogs are temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if err := json.NewEncoder(w).Encode(catalog); err != nil {
		return
	}
}

func (p *Provider) fetch(ctx context.Context) (Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	type result struct {
		minecraft []string
		neoForge  []string
		err       error
	}
	results := make(chan result, 2)
	go func() {
		versions, err := p.fetchMinecraft(ctx)
		results <- result{minecraft: versions, err: err}
	}()
	go func() {
		versions, err := p.fetchNeoForge(ctx)
		results <- result{neoForge: versions, err: err}
	}()

	var catalog Catalog
	for i := 0; i < 2; i++ {
		item := <-results
		if item.err != nil {
			return Catalog{}, item.err
		}
		if item.minecraft != nil {
			catalog.Minecraft = item.minecraft
		} else {
			catalog.NeoForge = item.neoForge
		}
	}
	if len(catalog.Minecraft) == 0 || len(catalog.NeoForge) == 0 {
		return Catalog{}, errors.New("upstream returned an empty game version catalog")
	}
	catalog.UpdatedAt = time.Now().UTC()
	return catalog, nil
}

func (p *Provider) fetchMinecraft(ctx context.Context) ([]string, error) {
	var manifest struct {
		Versions []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"versions"`
	}
	if err := p.getJSON(ctx, p.minecraftURL, &manifest); err != nil {
		return nil, fmt.Errorf("read Minecraft version manifest: %w", err)
	}
	versions := make([]string, 0, len(manifest.Versions))
	seen := make(map[string]struct{}, len(manifest.Versions))
	for _, version := range manifest.Versions {
		if version.Type != "release" || !validDottedVersion(version.ID) {
			continue
		}
		if _, ok := seen[version.ID]; ok {
			continue
		}
		seen[version.ID] = struct{}{}
		versions = append(versions, version.ID)
		if len(versions) > maxCatalogEntries {
			return nil, errors.New("Minecraft release catalog exceeds entry limit")
		}
	}
	if len(versions) == 0 {
		return nil, errors.New("Minecraft version manifest contains no releases")
	}
	return versions, nil
}

func (p *Provider) fetchNeoForge(ctx context.Context) ([]string, error) {
	// NeoForged Maven's metadata file can contain only recent prereleases. Its
	// public artifact index lists the complete set of stable version dirs.
	indexURL := p.neoForgeURL
	if strings.HasSuffix(indexURL, "maven-metadata.xml") {
		indexURL = strings.TrimSuffix(indexURL, "maven-metadata.xml")
	} else {
		indexURL = strings.TrimRight(indexURL, "/") + "/"
	}
	var versions []string
	seen := make(map[string]struct{})
	body, indexErr := p.get(ctx, indexURL)
	if indexErr == nil {
		for _, match := range neoForgeVersionDirectory.FindAllSubmatch(body, -1) {
			version := string(match[1])
			if !validDottedVersion(version) {
				continue
			}
			if _, exists := seen[version]; exists {
				continue
			}
			seen[version] = struct{}{}
			versions = append(versions, version)
			if len(versions) > maxCatalogEntries {
				return nil, errors.New("NeoForge version index exceeds entry limit")
			}
		}
	}
	if len(versions) == 0 {
		var metadata struct {
			Versioning struct {
				Versions []string `xml:"versions>version"`
			} `xml:"versioning"`
		}
		metadataErr := p.getXML(ctx, p.neoForgeURL, &metadata)
		for _, version := range metadata.Versioning.Versions {
			if !validDottedVersion(version) {
				continue
			}
			if _, exists := seen[version]; exists {
				continue
			}
			seen[version] = struct{}{}
			versions = append(versions, version)
			if len(versions) > maxCatalogEntries {
				return nil, errors.New("NeoForge Maven metadata exceeds entry limit")
			}
		}
		if len(versions) == 0 && metadataErr != nil && indexErr != nil {
			return nil, fmt.Errorf("read NeoForge version index (%v) and Maven metadata: %w", indexErr, metadataErr)
		}
		if len(versions) == 0 {
			return nil, errors.New("NeoForge Maven index and metadata contain no stable versions")
		}
	}
	sort.SliceStable(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) > 0 })
	return versions, nil
}

func (p *Provider) getJSON(ctx context.Context, url string, dst any) error {
	body, err := p.get(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode JSON metadata: %w", err)
	}
	return nil
}

func (p *Provider) getXML(ctx context.Context, url string, dst any) error {
	body, err := p.get(ctx, url)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode XML metadata: %w", err)
	}
	return nil
}

func (p *Provider) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxMetadataBytes {
		return nil, errors.New("upstream metadata exceeds size limit")
	}
	return body, nil
}

func validDottedVersion(value string) bool {
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}

func compareVersions(left, right string) int {
	a, b := strings.Split(left, "."), strings.Split(right, ".")
	length := len(a)
	if len(b) > length {
		length = len(b)
	}
	for i := 0; i < length; i++ {
		var ai, bi uint64
		if i < len(a) {
			ai, _ = strconv.ParseUint(a[i], 10, 64)
		}
		if i < len(b) {
			bi, _ = strconv.ParseUint(b[i], 10, 64)
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func clone(source Catalog) Catalog {
	return Catalog{
		Minecraft: append([]string(nil), source.Minecraft...),
		NeoForge:  append([]string(nil), source.NeoForge...),
		UpdatedAt: source.UpdatedAt,
	}
}
