package publictrackers

import (
	"context"
	"jacred/app"
	"jacred/filedb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const bayFixture = `[{"id":"12","info_hash":"0123456789abcdef0123456789abcdef01234567","name":"Example Movie (2026) 1080p WEB-DL","category":"207","size":"1073741824","seeders":"12","leechers":"3","added":"1780000000"},{"id":13,"info_hash":"abcdef0123456789abcdef0123456789abcdef01","name":"Example Show S01E02 1080p","category":208,"size":1024,"seeders":4,"leechers":1,"added":1780000000},{"id":14,"category":501,"name":"excluded","info_hash":"0123456789abcdef0123456789abcdef01234567"}]`

func TestBayFieldsAndCategoryFilter(t *testing.T) {
	rows, err := parseBay([]byte(bayFixture))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0]["sid"] != 12 || rows[0]["pir"] != 3 || rows[0]["size"] != int64(1073741824) || rows[0]["name"] != "Example Movie" || rows[0]["relased"] != 2026 {
		t.Fatalf("wrong row: %+v", rows[0])
	}
	if rows[1]["types"].([]string)[0] != "serial" {
		t.Fatal("TV category lost")
	}
	for _, bad := range []string{`[{"id":"0","name":"No results"}]`, `<html>blocked</html>`, `[{"id":4,"category":207,"name":"bad","info_hash":"invalid"}]`} {
		if _, err := parseBay([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func Test1337ListingAndDetail(t *testing.T) {
	body := `<table><tr><td class="coll-1 name"><a href="/torrent/42/Example-Movie-2026/">Example Movie (2026) 2160p</a></td><td class="coll-2 seeds">30</td><td class="coll-3 leeches">4</td><td class="coll-date">Nov. 2nd '26</td><td class="coll-4 size">2.5 GB<span>30</span></td></tr></table>`
	rows, err := parse1337(body, "https://1337x.to", "Movies")
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	r := rows[0]
	if r["size"] != int64(2.5*(1<<30)) || r["sid"] != 30 || r["pir"] != 4 || r["name"] != "Example Movie" {
		t.Fatalf("wrong row %+v", r)
	}
	if !strings.HasPrefix(r["createTime"].(string), "2026-11-02") {
		t.Fatal(r["createTime"])
	}
	magnet := findMagnet(`<ul><li><a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;dn=example">download</a></li></ul>`)
	if !strings.Contains(magnet, "&dn=example") {
		t.Fatal(magnet)
	}
	if _, err := parse1337("<html>Just a moment</html>", "https://1337x.to", "TV"); err == nil {
		t.Fatal("challenge accepted as empty success")
	}
	stamp := listingDate("11:25am", time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	if stamp.Day() != 3 || stamp.Month() != 11 || stamp.Year() != 2026 {
		t.Fatal(stamp)
	}
}

func TestBayCollectionPersistsAndUpdatesCounts(t *testing.T) {
	body := bayFixture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/precompiled/data_top100_200.json" {
			t.Error(r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	cfg := app.DefaultConfig()
	cfg.PirateBay.Host = srv.URL
	cfg.PirateBay.ReqMinute = 0
	cfg.PirateBay.ParseDelay = 0
	db := filedb.New(cfg, t.TempDir())
	p := New(db)
	r, err := p.Parse(context.Background(), "piratebay", cfg, 1)
	if err != nil || r.Added != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	body = strings.Replace(body, `"seeders":"12"`, `"seeders":"22"`, 1)
	r, err = p.Parse(context.Background(), "piratebay", cfg, 1)
	if err != nil || r.Updated != 1 || r.Added != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	rows, err := db.Search("", "Example Movie", "Example Movie", 2026, 1)
	if err != nil || len(rows) != 1 || rows[0]["sid"] != 22 {
		t.Fatalf("%+v %v", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Parse(ctx, "piratebay", cfg, 1); err != context.Canceled {
		t.Fatal(err)
	}
}
