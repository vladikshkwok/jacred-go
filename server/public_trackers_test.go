package server

import (
	"net/http/httptest"
	"testing"
)

func TestPublicTrackerRoutesRequireAdminCredentials(t *testing.T) {
	s := releaseServer(t)
	s.Config.DevKey = "admin-test"
	s.Config.DisableTrackers = []string{"piratebay", "1337x"}
	h := s.Handler()
	for _, source := range []string{"piratebay", "1337x"} {
		path := "/cron/" + source + "/parse"
		for _, auth := range []bool{false, true} {
			r := httptest.NewRequest("GET", path, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			if auth {
				r.Header.Set("X-Api-Key", "test-key")
				r.Header.Set("X-Dev-Key", "admin-test")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if auth && w.Code != 200 {
				t.Fatalf("disabled admin source %s: %d %s", source, w.Code, w.Body.String())
			}
			if !auth && w.Code == 200 {
				t.Fatalf("public access to collection route %s", source)
			}
		}
	}
}
