package channeldna

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/epglink"
)

func Compute(ch catalog.LiveChannel) string {
	if dna := strings.TrimSpace(ch.DNAID); dna != "" {
		return dna
	}
	key := identityKey(ch)
	sum := sha1.Sum([]byte(key))
	return "dna-" + hex.EncodeToString(sum[:8])
}

func Assign(live []catalog.LiveChannel) {
	for i := range live {
		live[i].DNAID = Compute(live[i])
	}
}

func identityKey(ch catalog.LiveChannel) string {
	if tvg := strings.ToLower(strings.TrimSpace(ch.TVGID)); tvg != "" {
		return "tvgid:" + tvg
	}
	norm := epglink.NormalizeName(ch.GuideName)
	if norm == "" {
		norm = epglink.NormalizeName(ch.ChannelID)
	}
	guide := strings.TrimSpace(ch.GuideNumber)
	switch {
	case norm != "" && guide != "":
		return "name-guide:" + norm + ":" + guide
	case norm != "":
		return "name:" + norm
	case guide != "":
		return "guide:" + guide
	case strings.TrimSpace(ch.ChannelID) != "":
		return "channel:" + strings.ToLower(strings.TrimSpace(ch.ChannelID))
	default:
		return "unknown"
	}
}
