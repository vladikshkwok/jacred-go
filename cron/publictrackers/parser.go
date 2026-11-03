// Package publictrackers collects bounded recent movie/TV listings. It never
// executes site scripts; HTML requests share the existing challenge-aware fetcher.
package publictrackers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"jacred/app"
	"jacred/core"
	"jacred/cron/knaben"
	"jacred/filedb"
	"jacred/tracks"
)

type Result struct {
	Status  string `json:"status"`
	Fetched int    `json:"fetched"`
	Added   int    `json:"added"`
	Updated int    `json:"updated"`
	Skipped int    `json:"skipped"`
	Failed  int    `json:"failed"`
}

type Parser struct {
	DB   *filedb.DB
	mu   sync.Mutex
	busy map[string]bool
}

func New(db *filedb.DB) *Parser { return &Parser{DB: db, busy: map[string]bool{}} }

func (p *Parser) Parse(ctx context.Context, source string, cfg app.Config, pages int) (Result, error) {
	r := Result{Status: "ok"}
	for _, name := range cfg.DisableTrackers {
		if strings.EqualFold(name, source) {
			r.Status = "disabled"
			return r, nil
		}
	}
	p.mu.Lock()
	if p.busy[source] {
		p.mu.Unlock()
		r.Status = "work"
		return r, nil
	}
	p.busy[source] = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.busy, source); p.mu.Unlock() }()
	ts := cfg.PirateBay
	if source == "1337x" {
		ts = cfg.X1337
	} else if source != "piratebay" {
		return r, fmt.Errorf("unknown source")
	}
	if ts.Host == "" {
		return r, fmt.Errorf("%s: host missing", source)
	}
	fetcher := core.NewFetcher(cfg)
	get := func(path string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		delay := time.Duration(max(ts.ParseDelay, 1000)) * time.Millisecond
		if source == "1337x" {
			delay = max(delay, 3*time.Second)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
		body, status, err := fetcher.GetString(strings.TrimRight(ts.Host, "/")+path, ts)
		if err != nil {
			return "", err
		}
		if status != 200 {
			return "", fmt.Errorf("%s HTTP %d", source, status)
		}
		return body, nil
	}
	var rows []filedb.TorrentDetails
	if source == "piratebay" {
		body, err := get("/precompiled/data_top100_200.json")
		if err != nil {
			return r, err
		}
		rows, err = parseBay([]byte(body))
		if err != nil {
			return r, err
		}
	} else {
		for _, cat := range []string{"Movies", "TV"} {
			for page := 1; page <= min(max(pages, 1), 3); page++ {
				body, err := get(fmt.Sprintf("/cat/%s/%d/", cat, page))
				if err != nil {
					return r, err
				}
				list, err := parse1337(body, ts.Host, cat)
				if err != nil {
					return r, err
				}
				for _, row := range list {
					body, err = get(strings.TrimPrefix(row["url"].(string), strings.TrimRight(ts.Host, "/")))
					if err != nil {
						return r, err
					}
					magnet := findMagnet(body)
					if _, err := tracks.InfoHashFromMagnet(magnet); err != nil {
						r.Failed++
						continue
					}
					row["magnet"] = magnet
					rows = append(rows, row)
				}
			}
		}
	}
	r.Fetched = len(rows)
	if r.Fetched == 0 && r.Failed > 0 {
		return r, fmt.Errorf("%s: no usable magnets in listing details", source)
	}
	// Lock each bucket over read/merge/write: several sources may publish the
	// same film concurrently. Never replace another crawler's freshly saved row.
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		key := p.DB.KeyDb(row["name"].(string), row["originalname"].(string))
		bucket, unlock, err := p.DB.OpenReadOrEmptyLocked(key)
		if err != nil {
			return r, err
		}
		link := row["url"].(string)
		merged := filedb.MergeTorrent(bucket[link], row, cfg.TracksAttempt)
		if !merged.Changed {
			r.Skipped++
			unlock()
			continue
		}
		bucket[link] = merged.Torrent
		err = p.DB.SaveBucketUnlocked(key, bucket, time.Now().UTC())
		unlock()
		if err != nil {
			return r, err
		}
		if merged.IsNew {
			r.Added++
		} else {
			r.Updated++
		}
	}
	return r, nil
}

// Flexible integers: apibay serves both string and numeric fields.
type number int64

func (n *number) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), "\""), 10, 64)
	*n = number(v)
	return err
}

type bayRow struct {
	ID       number `json:"id"`
	Hash     string `json:"info_hash"`
	Name     string `json:"name"`
	Category number `json:"category"`
	Size     number `json:"size"`
	Seeders  number `json:"seeders"`
	Leechers number `json:"leechers"`
	Added    number `json:"added"`
}

func parseBay(b []byte) ([]filedb.TorrentDetails, error) {
	var hits []bayRow
	if err := json.Unmarshal(b, &hits); err != nil {
		return nil, fmt.Errorf("piratebay JSON: %w", err)
	}
	var out []filedb.TorrentDetails
	for _, h := range hits {
		if h.ID == 0 {
			continue
		} // API's no-results sentinel.
		var typ string
		switch h.Category {
		case 201, 202, 207, 209, 210, 211:
			typ = "movie"
		case 205, 208, 212:
			typ = "serial"
		default:
			continue
		}
		hash, err := hex.DecodeString(h.Hash)
		if err != nil || len(hash) != 20 || strings.TrimSpace(h.Name) == "" {
			continue
		}
		row := record("piratebay", h.Name, fmt.Sprintf("https://thepiratebay.org/description.php?id=%d", h.ID), typ, int64(h.Size), int(h.Seeders), int(h.Leechers), time.Unix(int64(h.Added), 0))
		row["magnet"] = "magnet:?xt=urn:btih:" + strings.ToLower(h.Hash) + "&dn=" + url.QueryEscape(h.Name)
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("piratebay: no valid movie/TV records")
	}
	return out, nil
}
func record(source, title, link, typ string, size int64, seeds, peers int, stamp time.Time) filedb.TorrentDetails {
	name, year := knaben.ParseNameAndYear(title)
	return filedb.TorrentDetails{"trackerName": source, "title": title, "name": name, "originalname": name, "relased": year, "url": link, "types": []string{typ}, "size": size, "sizeName": fmt.Sprintf("%.2f GB", float64(size)/(1<<30)), "sid": seeds, "pir": peers, "createTime": stamp.UTC().Format(time.RFC3339), "updateTime": time.Now().UTC().Format(time.RFC3339)}
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func visit(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		visit(c, f)
	}
}
func textOf(n *html.Node) string {
	var b strings.Builder
	visit(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	})
	return strings.TrimSpace(b.String())
}
func parse1337(body, host, category string) ([]filedb.TorrentDetails, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []filedb.TorrentDetails
	visit(doc, func(tr *html.Node) {
		if tr.Data != "tr" {
			return
		}
		var title, link, size, date string
		seeds, peers := 0, 0
		visit(tr, func(n *html.Node) {
			if n.Data == "a" && strings.HasPrefix(attr(n, "href"), "/torrent/") {
				title = textOf(n)
				link = attr(n, "href")
			}
			if n.Data == "td" {
				cls := attr(n, "class")
				switch {
				case strings.Contains(cls, "coll-2"):
					seeds, _ = strconv.Atoi(textOf(n))
				case strings.Contains(cls, "coll-3"):
					peers, _ = strconv.Atoi(textOf(n))
				case strings.Contains(cls, "coll-4"):
					// Ignore the uploader/seed-span nested in the size cell.
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.TextNode {
							size += c.Data
						}
					}
				case strings.Contains(cls, "coll-date"):
					date = textOf(n)
				}
			}
		})
		if title == "" || link == "" {
			return
		}
		if strings.Contains(title, "...") {
			parts := strings.Split(strings.Trim(link, "/"), "/")
			title = strings.ReplaceAll(parts[len(parts)-1], "-", " ")
		}
		typ := "movie"
		if category == "TV" {
			typ = "serial"
		}
		out = append(out, record("1337x", title, strings.TrimRight(host, "/")+link, typ, parseSize(size), seeds, peers, listingDate(date, time.Now().UTC())))
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("1337x: listing contains no torrent rows")
	}
	return out, nil
}
func listingDate(s string, now time.Time) time.Time {
	s = strings.NewReplacer("st", "", "nd", "", "rd", "", "th", "").Replace(strings.TrimSpace(s))
	for _, layout := range []string{"Jan. 2 '06", "3pm Jan. 2", "3:04pm Jan. 2", "3:04pm"} {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Year() == 0 {
				t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
				if layout == "3:04pm" {
					t = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
				}
				if t.After(now.Add(24 * time.Hour)) {
					t = t.AddDate(-1, 0, 0)
				}
			}
			return t
		}
	}
	return now
}
func parseSize(s string) int64 {
	f := strings.Fields(s)
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.ReplaceAll(f[0], ",", ""), 64)
	switch strings.ToUpper(f[1]) {
	case "KB", "KIB":
		v *= 1 << 10
	case "MB", "MIB":
		v *= 1 << 20
	case "GB", "GIB":
		v *= 1 << 30
	case "TB", "TIB":
		v *= 1 << 40
	}
	return int64(v)
}
func findMagnet(body string) string {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	var found string
	visit(doc, func(n *html.Node) {
		if n.Data == "a" && strings.HasPrefix(attr(n, "href"), "magnet:?xt=urn:btih:") && found == "" {
			found = attr(n, "href")
		}
	})
	return found
}
