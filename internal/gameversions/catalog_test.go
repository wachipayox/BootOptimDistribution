package gameversions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderFetchesOfficialReleasesAndCaches(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/minecraft" {
			_, _ = w.Write([]byte(`{"versions":[{"id":"1.21.10","type":"release"},{"id":"24w01a","type":"snapshot"},{"id":"1.21.1","type":"release"},{"id":"1.21.1","type":"release"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<metadata><versioning><versions><version>21.1.9</version><version>21.1.197</version><version>21.1.10</version></versions></versioning></metadata>`))
	}))
	defer upstream.Close()

	provider := NewProvider(upstream.Client(), upstream.URL+"/minecraft", upstream.URL+"/neoforge", time.Hour)
	first, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if want := []string{"1.21.10", "1.21.1"}; !reflect.DeepEqual(first.Minecraft, want) {
		t.Fatalf("Minecraft = %#v, want %#v", first.Minecraft, want)
	}
	if want := []string{"21.1.197", "21.1.10", "21.1.9"}; !reflect.DeepEqual(first.NeoForge, want) {
		t.Fatalf("NeoForge = %#v, want %#v", first.NeoForge, want)
	}
	if first.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt is empty")
	}
	first.Minecraft[0] = "mutated"
	second, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("cached Get(): %v", err)
	}
	if second.Minecraft[0] != "1.21.10" {
		t.Fatalf("cached catalog was mutable: %#v", second.Minecraft)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("upstream request count = %d, want 2 for one refresh", got)
	}
}

func TestProviderHandlerBoundsMethodsAndServesJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/minecraft" {
			_, _ = w.Write([]byte(`{"versions":[{"id":"1.21.1","type":"release"}]}`))
			return
		}
		_, _ = w.Write([]byte(`<metadata><versioning><versions><version>21.1.197</version></versions></versioning></metadata>`))
	}))
	defer upstream.Close()
	provider := NewProvider(upstream.Client(), upstream.URL+"/minecraft", upstream.URL+"/neoforge", time.Hour)

	methodRecorder := httptest.NewRecorder()
	provider.ServeHTTP(methodRecorder, httptest.NewRequest(http.MethodPost, "/admin/api/game-versions", nil))
	if methodRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", methodRecorder.Code, http.StatusMethodNotAllowed)
	}

	recorder := httptest.NewRecorder()
	provider.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/game-versions", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var catalog Catalog
	if err := json.Unmarshal(recorder.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(catalog.Minecraft) != 1 || len(catalog.NeoForge) != 1 {
		t.Fatalf("unexpected response: %#v", catalog)
	}
}

func TestProviderRejectsUnusableUpstreamMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"versions":[{"id":"24w01a","type":"snapshot"}]}`))
	}))
	defer upstream.Close()
	provider := NewProvider(upstream.Client(), upstream.URL, upstream.URL, time.Hour)
	if _, err := provider.Get(context.Background()); err == nil {
		t.Fatal("Get() succeeded with catalogs containing no usable releases")
	}
}
