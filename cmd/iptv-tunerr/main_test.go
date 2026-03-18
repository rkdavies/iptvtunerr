package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/config"
)

func TestApplyRuntimeEPGRepairs_ExternalRepairsIncorrectTVGID(t *testing.T) {
	xmltv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><tv>
<channel id="foxnews.us"><display-name>FOX News Channel</display-name></channel>
</tv>`))
	}))
	defer xmltv.Close()

	cfg := &config.Config{
		XMLTVURL:         xmltv.URL,
		XMLTVMatchEnable: true,
	}
	live := []catalog.LiveChannel{
		{ChannelID: "1", GuideName: "FOX News Channel US", TVGID: "wrong.id", EPGLinked: true},
	}
	applyRuntimeEPGRepairs(cfg, live, "", "", "")
	if got := live[0].TVGID; got != "foxnews.us" {
		t.Fatalf("TVGID=%q want foxnews.us", got)
	}
	if !live[0].EPGLinked {
		t.Fatal("EPGLinked should remain true")
	}
}

func TestApplyRuntimeEPGRepairs_PrefersProviderBeforeExternal(t *testing.T) {
	providerXMLTV := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><tv>
<channel id="provider.foxnews"><display-name>FOX News Channel</display-name></channel>
</tv>`))
	}))
	defer providerXMLTV.Close()

	externalXMLTV := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><tv>
<channel id="external.foxnews"><display-name>FOX News Channel</display-name></channel>
</tv>`))
	}))
	defer externalXMLTV.Close()

	cfg := &config.Config{
		ProviderEPGEnabled: true,
		XMLTVURL:           externalXMLTV.URL,
		XMLTVMatchEnable:   true,
	}
	live := []catalog.LiveChannel{
		{ChannelID: "1", GuideName: "FOX News Channel US", TVGID: "wrong.id", EPGLinked: true},
	}
	applyRuntimeEPGRepairs(cfg, live, providerXMLTV.URL, "u", "p")
	if got := live[0].TVGID; got != "provider.foxnews" {
		t.Fatalf("TVGID=%q want provider.foxnews", got)
	}
}
