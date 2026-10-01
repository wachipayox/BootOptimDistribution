package profileapi

import (
	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"strings"
	"testing"
)

func TestProfileIconIsPublishedCASObject(t *testing.T) {
	api := API{maxObjectBytes: 10 * 1024 * 1024}
	digest := strings.Repeat("a", 64)
	objects, err := api.expectedObjects(revision.Manifest{Profile: revision.Profile{Icon: &revision.ObjectRef{SHA256: digest, Size: 100, MediaType: "image/png"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].SHA256 != digest || objects[0].Size != 100 {
		t.Fatalf("icon not registered as published object: %+v", objects)
	}
}
