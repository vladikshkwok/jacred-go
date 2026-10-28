package server

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"jacred/filedb"
)

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type torznabItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Date      string `xml:"pubDate,omitempty"`
	Category  []int  `xml:"category"`
	Indexer   string `xml:"jackettindexer"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
	Attributes []torznabAttr `xml:"torznab:attr"`
}

func writeXML(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	if err := xml.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, "Unable to encode XML response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write(buf.Bytes())
}

func torznabError(w http.ResponseWriter, code, description string) {
	writeXML(w, struct {
		XMLName     xml.Name `xml:"error"`
		Code        string   `xml:"code,attr"`
		Description string   `xml:"description,attr"`
	}{Code: code, Description: description})
}

func (s *Server) handleTorznab(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	action := strings.ToLower(defaultString(q.Get("t"), "search"))
	if action == "caps" {
		// Fixed protocol metadata; never interpolate request Host or credentials.
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Write([]byte(xml.Header + `<caps><server title="Jacred-go"/><limits max="2000" default="100"/><searching><search available="yes" supportedParams="q,year"/><tv-search available="yes" supportedParams="q,year"/><movie-search available="yes" supportedParams="q,year"/></searching><categories><category id="2000" name="Movies"/><category id="5000" name="TV"><subcat id="5070" name="Anime"/></category></categories></caps>`))
		return
	}
	if action != "search" && action != "movie" && action != "moviesearch" && action != "tvsearch" {
		torznabError(w, "202", "Unsupported search function")
		return
	}
	for _, key := range []string{"imdbid", "tvdbid", "tmdbid", "season", "ep"} {
		if q.Get(key) != "" {
			torznabError(w, "201", "Unsupported search parameter: "+key)
			return
		}
	}
	limit, offset := 100, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if value := q.Get(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || (key == "limit" && n == 0) {
				torznabError(w, "201", "Invalid pagination")
				return
			}
			*target = n
		}
	}
	if limit > 2000 {
		limit = 2000
	}
	isSerial := -1
	if action == "movie" || action == "moviesearch" {
		isSerial = 1
	} else if action == "tvsearch" {
		isSerial = 2
	}
	res, err := s.searchReleases(filedb.SearchParams{
		APIKey: getAPIKey(r), Query: firstQueryURL(q, "q", "query", "Query"),
		Title: q.Get("title"), TitleOriginal: q.Get("title_original"),
		Year: atoi(q.Get("year")), IsSerial: isSerial, CategoryRaw: firstQueryURL(q, "cat", "category"),
	})
	if err != nil {
		torznabError(w, "900", "Search failed")
		return
	}
	items := make([]torznabItem, 0, len(res.Results))
	for _, t := range res.Results {
		link := asString(t["magnet"])
		if !strings.HasPrefix(link, "magnet:?") {
			// A tracker details page is not a downloadable torrent enclosure.
			continue
		}
		cats, _ := categories(t)
		item := torznabItem{Title: asString(t["title"]), GUID: asString(t["url"]), Link: link, Category: cats, Indexer: asString(t["trackerName"])}
		if date, err := time.Parse(time.RFC3339Nano, asString(t["createTime"])); err == nil {
			item.Date = date.UTC().Format(time.RFC1123Z)
		}
		item.Enclosure.URL, item.Enclosure.Type = link, "application/x-bittorrent"
		item.Enclosure.Length = int64(parseSize(t))
		item.Attributes = []torznabAttr{{"seeders", strconv.Itoa(asInt(t["sid"]))}, {"peers", strconv.Itoa(asInt(t["pir"]))}, {"size", strconv.FormatInt(item.Enclosure.Length, 10)}}
		if u, err := url.Parse(link); err == nil {
			if hash := strings.TrimPrefix(u.Query().Get("xt"), "urn:btih:"); hash != "" {
				item.Attributes = append(item.Attributes, torznabAttr{"infohash", hash})
			}
		}
		for _, cat := range cats {
			item.Attributes = append(item.Attributes, torznabAttr{"category", strconv.Itoa(cat)})
		}
		items = append(items, item)
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := total
	if limit < total-offset {
		end = offset + limit
	}
	feed := struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Torznab string   `xml:"xmlns:torznab,attr"`
		Channel struct {
			Title    string `xml:"title"`
			Response struct {
				Offset int `xml:"offset,attr"`
				Total  int `xml:"total,attr"`
			} `xml:"torznab:response"`
			Items []torznabItem `xml:"item"`
		} `xml:"channel"`
	}{Version: "2.0", Torznab: "http://torznab.com/schemas/2015/feed"}
	feed.Channel.Title = "Jacred-go"
	feed.Channel.Response.Offset, feed.Channel.Response.Total = offset, total
	feed.Channel.Items = items[offset:end]
	writeXML(w, feed)
}
