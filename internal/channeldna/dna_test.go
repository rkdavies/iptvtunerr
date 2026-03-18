package channeldna

import (
	"testing"

	"github.com/snapetech/iptvtunerr/internal/catalog"
)

func TestComputePrefersTVGIDStability(t *testing.T) {
	a := Compute(catalog.LiveChannel{GuideName: "FOX News HD", TVGID: "foxnews.us", GuideNumber: "101"})
	b := Compute(catalog.LiveChannel{GuideName: "FOX News Channel US", TVGID: "foxnews.us", GuideNumber: "42"})
	if a != b {
		t.Fatalf("dna mismatch for same tvgid: %q vs %q", a, b)
	}
}

func TestComputeFallsBackToNormalizedNameAndGuide(t *testing.T) {
	a := Compute(catalog.LiveChannel{GuideName: "Nick Junior Canada HD", GuideNumber: "12"})
	b := Compute(catalog.LiveChannel{GuideName: "Nick Junior Canada", GuideNumber: "12"})
	if a != b {
		t.Fatalf("dna mismatch for normalized equivalent names: %q vs %q", a, b)
	}
}
