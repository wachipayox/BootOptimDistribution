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
	defaultFabricGame   = "https://meta.fabricmc.net/v2/versions/game"
	defaultFabricLoader = "https://meta.fabricmc.net/v2/versions/loader"
	defaultForgeURL     = "https://maven.minecraftforge.net/net/minecraftforge/forge/maven-metadata.xml"
	maxMetadataBytes    = 8 << 20
	maxCatalogEntries   = 5000
	fetchTimeout        = 20 * time.Second
)

var neoForgeVersionDirectory = regexp.MustCompile(`href="\./([0-9]+(?:\.[0-9]+)+)/"`)

// Catalog is the official release list consumed by the administrator's
// searchable version pickers. NeoForge versions are ordered newest first.
//
// Fabric loaders work with every Minecraft version Fabric supports, so Fabric is
// two lists: the supported Minecraft releases and the stable loader versions.
// Forge entries are full Maven versions ("1.21.1-52.0.30", "1.7.10-10.13.4.1614-1.7.10"):
// the Minecraft version is the text before the first dash, and the whole string
// is what the manifest and launchers use as game.loader.version.
type Catalog struct {
	Minecraft  []string  `json:"minecraft"`
	NeoForge   []string  `json:"neoforge"`
	Fabric     []string  `json:"fabric"`
	FabricGame []string  `json:"fabric_game"`
	Forge      []string  `json:"forge"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Sources are the upstream metadata URLs. NewProvider leaves Fabric and Forge
// disabled (used by tests); NewProviderFromSources fills every empty one in.
type Sources struct {
	Minecraft, NeoForge, FabricGame, FabricLoader, Forge string
}

// Provider fetches official metadata and keeps a short in-memory cache so
// opening the profile form does not contact upstream services on every visit.
type Provider struct {
	client       *http.Client
	minecraftURL string
	neoForgeURL  string
	sources      Sources
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

// NewProviderFromSources is NewProvider with Fabric and Forge enabled; empty
// fields take the official default URL.
func NewProviderFromSources(client *http.Client, sources Sources, ttl time.Duration) *Provider {
	p := NewProvider(client, sources.Minecraft, sources.NeoForge, ttl)
	p.sources = Sources{FabricGame: sources.FabricGame, FabricLoader: sources.FabricLoader, Forge: sources.Forge}
	if p.sources.FabricGame == "" {
		p.sources.FabricGame = defaultFabricGame
	}
	if p.sources.FabricLoader == "" {
		p.sources.FabricLoader = defaultFabricLoader
	}
	if p.sources.Forge == "" {
		p.sources.Forge = defaultForgeURL
	}
	return p
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
		p.cached = keepLastGoodLoaders(fresh, p.cached)
		p.expires = time.Now().Add(p.ttl)
		return clone(p.cached), nil
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
	// Fabric and Forge are best effort: an outage there must not hide NeoForge,
	// and Get keeps the last good list for a loader whose refresh failed.
	if p.sources.FabricGame != "" && p.sources.FabricLoader != "" {
		if game, loaders, err := p.fetchFabric(ctx); err == nil {
			catalog.FabricGame, catalog.Fabric = game, loaders
		}
	}
	if p.sources.Forge != "" {
		if versions, err := p.fetchForge(ctx); err == nil {
			catalog.Forge = versions
		}
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

func keepLastGoodLoaders(fresh, previous Catalog) Catalog {
	if len(fresh.Fabric) == 0 || len(fresh.FabricGame) == 0 {
		fresh.Fabric, fresh.FabricGame = previous.Fabric, previous.FabricGame
	}
	if len(fresh.Forge) == 0 {
		fresh.Forge = previous.Forge
	}
	return fresh
}

func clone(source Catalog) Catalog {
	return Catalog{
		Minecraft:  append([]string{}, source.Minecraft...),
		NeoForge:   append([]string{}, source.NeoForge...),
		Fabric:     append([]string{}, source.Fabric...),
		FabricGame: append([]string{}, source.FabricGame...),
		Forge:      append([]string{}, source.Forge...),
		UpdatedAt:  source.UpdatedAt,
	}
}

func (p *Provider) fetchFabric(ctx context.Context) (game, loaders []string, err error) {
	var gameList, loaderList []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := p.getJSON(ctx, p.sources.FabricGame, &gameList); err != nil {
		return nil, nil, fmt.Errorf("read Fabric game versions: %w", err)
	}
	if err := p.getJSON(ctx, p.sources.FabricLoader, &loaderList); err != nil {
		return nil, nil, fmt.Errorf("read Fabric loader versions: %w", err)
	}
	for _, item := range gameList {
		if item.Stable && validDottedVersion(item.Version) {
			game = append(game, item.Version)
		}
	}
	for _, item := range loaderList {
		if item.Stable && validLoaderVersion(item.Version) {
			loaders = append(loaders, item.Version)
		}
	}
	if len(game) == 0 || len(loaders) == 0 || len(game) > maxCatalogEntries || len(loaders) > maxCatalogEntries {
		return nil, nil, errors.New("Fabric metadata is empty or exceeds entry limit")
	}
	return game, loaders, nil
}

func (p *Provider) fetchForge(ctx context.Context) ([]string, error) {
	var metadata struct {
		Versioning struct {
			Versions []string `xml:"versions>version"`
		} `xml:"versioning"`
	}
	if err := p.getXML(ctx, p.sources.Forge, &metadata); err != nil {
		return nil, fmt.Errorf("read Forge Maven metadata: %w", err)
	}
	seen := make(map[string]struct{}, len(metadata.Versioning.Versions))
	var versions []string
	for _, version := range metadata.Versioning.Versions {
		// Keep entries shaped "<minecraft>-<forge>[-<minecraft>]" with a dotted
		// Minecraft part; branch and pre-release strings are not selectable builds.
		minecraft, _, ok := strings.Cut(version, "-")
		if !ok || !validDottedVersion(minecraft) || !validLoaderVersion(version) {
			continue
		}
		if _, dup := seen[version]; dup {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	if len(versions) == 0 || len(versions) > 4*maxCatalogEntries {
		return nil, errors.New("Forge Maven metadata is empty or exceeds entry limit")
	}
	sort.SliceStable(versions, func(i, j int) bool { return compareLoose(versions[i], versions[j]) > 0 })
	return versions, nil
}

// validLoaderVersion mirrors the manifest rule for game.loader.version.
func validLoaderVersion(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		alnum := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !alnum && (i == 0 || !strings.ContainsRune("._+-", rune(c))) {
			return false
		}
	}
	return true
}

// compareLoose orders dotted/dashed versions numerically part by part; non-numeric
// parts sort below numbers and between themselves lexically.
func compareLoose(left, right string) int {
	split := func(v string) []string {
		return strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	}
	a, b := split(left), split(right)
	for i := 0; i < len(a) && i < len(b); i++ {
		an, aErr := strconv.ParseUint(a[i], 10, 64)
		bn, bErr := strconv.ParseUint(b[i], 10, 64)
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aErr == nil:
			return 1
		case bErr == nil:
			return -1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return len(a) - len(b)
}
