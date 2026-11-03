package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJackettMetadataCacheIsolatedByClient(t *testing.T) {
	const rqUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/106.0.0.0 Safari/537.36"
	for _, order := range [][]string{{rqUA, "Lampa"}, {"Lampa", rqUA}} {
		s := releaseServer(t)
		key := s.DB.KeyDb("F1", "F1: The Movie")
		bucket, err := s.DB.OpenRead(key)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range bucket {
			row["ffprobe"] = []map[string]any{{"codec_type": "audio", "codec_name": "ac3", "channels": 6, "tags": map[string]string{"language": "rus"}}}
			row["languages"] = []string{"rus"}
		}
		if err := s.DB.SaveBucket(key, bucket, time.Now()); err != nil {
			t.Fatal(err)
		}
		for _, ua := range order {
			for repeat := 0; repeat < 2; repeat++ {
				r := httptest.NewRequest("GET", "/api/v2.0/indexers/all/results?apikey=test-key&Query=F1&year=2025", nil)
				r.Header.Set("User-Agent", ua)
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				var body struct{ Results []map[string]any }
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Results) == 0 {
					t.Fatalf("bad response: %d %s", w.Code, w.Body.String())
				}
				withTracks := false
				for _, row := range body.Results {
					withTracks = withTracks || row["ffprobe"] != nil
				}
				if withTracks != (ua == "Lampa") {
					t.Fatalf("metadata leaked between client modes (ua=%s repeat=%d): %s", ua, repeat, w.Body.String())
				}
			}
		}
	}
}
