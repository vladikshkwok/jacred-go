package tracks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"jacred/app"
	"jacred/filedb"
)

func TestQueuesStartInsideNightWindow(t *testing.T) {
	for _, task := range []int{1, 2, 5} {
		synctest.Test(t, func(t *testing.T) {
			cfg := app.DefaultConfig()
			cfg.Tracks = true
			cfg.TracksLog = true
			cfg.TracksInterval.Task0 = 360
			dir := t.TempDir()
			m := NewManager(cfg, filedb.New(cfg, dir), New(dir), dir, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go m.RunLoop(ctx, task)
			time.Sleep(21 * time.Second)
			synctest.Wait()
			b, err := os.ReadFile(filepath.Join(dir, "log", "tracks.log"))
			if err != nil || !strings.Contains(string(b), "collected 0 torrents") {
				t.Fatalf("queue %d did not scan after startup: %s %v", task, b, err)
			}
			cancel()
			synctest.Wait()
		})
	}
}

func TestCandidateCutoff(t *testing.T) {
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	for task, days := range map[int]int{1: 1, 2: 31, 5: 31} {
		if got := candidateCutoff(task, now); !got.Equal(now.AddDate(0, 0, -days)) {
			t.Fatalf("task %d: cutoff %s", task, got)
		}
	}
	for _, task := range []int{3, 4} {
		if !candidateCutoff(task, now).IsZero() {
			t.Fatalf("archive task %d must retain historical buckets", task)
		}
	}
}

func TestCollectCandidatesPartitionsAndSkipsSavedTracks(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.Tracks = true
	cfg.TracksAttempt = 3
	dir := t.TempDir()
	db := filedb.New(cfg, dir)
	tdb := New(dir)
	m := NewManager(cfg, db, tdb, dir, nil)
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	for i, age := range []time.Duration{time.Hour, 10 * 24 * time.Hour, 400 * 24 * time.Hour, time.Hour} {
		hash := strings.Repeat(string(rune('a'+i)), 40)
		stamp := now.Add(-age)
		updated := stamp
		if i == 2 { // Old release, recently updated: task 5.
			updated = now.Add(-time.Hour)
		}
		name := "film" + string(rune('a'+i))
		key := db.KeyDb(name, name)
		row := filedb.TorrentDetails{
			"name": name, "originalname": name, "title": name, "types": []string{"movie"},
			"magnet": "magnet:?xt=urn:btih:" + hash, "sid": 5,
			"createTime": stamp.Format(time.RFC3339), "updateTime": updated.Format(time.RFC3339),
		}
		if err := db.SaveBucket(key, map[string]filedb.TorrentDetails{name: row}, updated); err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			if err := tdb.Put(hash, FFProbeModel{Streams: []FFStream{{CodecType: "video", CodecName: "h264"}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for task, hash := range map[int]string{1: strings.Repeat("a", 40), 2: strings.Repeat("b", 40), 5: strings.Repeat("c", 40)} {
		got := m.CollectCandidates(context.Background(), task, now)
		if len(got) != 1 || !strings.Contains(got[0].Magnet, hash) {
			t.Fatalf("task %d: wrong candidates %+v", task, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ProcessOnce(ctx, 2, now); err != context.Canceled {
		t.Fatalf("canceled scan returned %v", err)
	}
}
