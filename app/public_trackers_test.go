package app

import "testing"

func TestPublicTrackerSettingsRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.X1337.Host = "https://1337x.st"
	cfg.X1337.FetchMode = "flaresolverr"
	cfg.PirateBay.Host = "https://apibay.org"
	parsed := DefaultConfig()
	parseYAMLIntoConfig(MarshalYAML(cfg), &parsed)
	if parsed.X1337 != cfg.X1337 || parsed.PirateBay != cfg.PirateBay {
		t.Fatal("public tracker settings lost during save")
	}
}
