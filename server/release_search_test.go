package server

import (
	"encoding/json"
	"encoding/xml"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"jacred/app"
	"jacred/filedb"
)

func releaseServer(t *testing.T) *Server {
	t.Helper()
	cfg := app.DefaultConfig()
	cfg.APIKey = "test-key"
	cfg.MergeDuplicates = false
	db := filedb.New(cfg, t.TempDir())
	for i, film := range []struct {
		name, original string
		year           int
	}{{"F1", "F1: The Movie", 2025}, {"Формула-1", "F1: The Movie", 2025}, {"F1", "F1 Classic", 2000}, {"Blade Runner 2049", "Blade Runner 2049", 2017}} {
		link := "https://example.test/" + film.name
		key := db.KeyDb(film.name, film.original)
		torrent := filedb.TorrentDetails{"title": film.name + " <4K> & DV", "name": film.name, "originalname": film.original, "types": []string{"movie"}, "trackerName": "rutor", "url": link, "magnet": "magnet:?xt=urn:btih:" + strings.Repeat(string(rune('a'+i)), 40) + "&dn=film", "relased": film.year, "sid": 10, "size": int64(1024), "createTime": time.Now().UTC().Format(time.RFC3339)}
		if err := db.SaveBucket(key, map[string]filedb.TorrentDetails{link: torrent}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{Config: cfg, DB: db, cache: newSearchCache(time.Minute, 100)}
}

func releaseCall(s *Server, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestJackettCaseAndTrailingYear(t *testing.T) {
	s := releaseServer(t)
	for _, parameter := range []string{"Query", "query", "q"} {
		w := releaseCall(s, "/api/v2.0/indexers/status:healthy/results?apikey=test-key&"+url.Values{parameter: {"F1 2025"}}.Encode())
		var body struct{ Results []map[string]any }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Results) != 2 {
			t.Fatalf("%s: expected both 2025 aliases, got %s", parameter, w.Body.String())
		}
	}
	w := releaseCall(s, "/api/v2.0/indexers/all/results?apikey=test-key&q=Blade+Runner+2049")
	var body struct{ Results []map[string]any }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatal("a genuine numeric title was stripped")
	}
}

func TestTorznabXMLAuthAndPagination(t *testing.T) {
	s := releaseServer(t)
	if w := releaseCall(s, "/torznab/api?t=caps"); w.Code != 401 {
		t.Fatalf("unauthorized caps: %d", w.Code)
	}
	for _, prefix := range []string{"/torznab/api", "/api/v2.0/indexers/all/results/torznab/api"} {
		w := releaseCall(s, prefix+"?apikey=test-key&t=caps")
		if !strings.Contains(w.Header().Get("Content-Type"), "xml") || !strings.Contains(w.Body.String(), "<caps>") {
			t.Fatalf("caps is not XML: %s", w.Body.String())
		}
	}
	w := releaseCall(s, "/torznab/api?apikey=test-key&t=movie&q=F1+2025&limit=1&offset=1")
	var rss struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
				Link  string `xml:"link"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &rss); err != nil {
		t.Fatal(err)
	}
	if len(rss.Channel.Items) != 1 || !strings.Contains(rss.Channel.Items[0].Title, "<4K> & DV") || !strings.Contains(rss.Channel.Items[0].Link, "&dn=film") {
		t.Fatalf("bad escaped/paginated feed: %s", w.Body.String())
	}
	for _, query := range []string{"limit=-1", "offset=999999999999999999999999999", "t=unknown", "season=1"} {
		w := releaseCall(s, "/torznab/api?apikey=test-key&"+query)
		if !strings.Contains(w.Body.String(), "<error") {
			t.Fatalf("expected XML error: %s", w.Body.String())
		}
	}
}
