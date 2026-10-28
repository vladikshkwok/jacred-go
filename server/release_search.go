package server

import (
	"regexp"
	"strconv"
	"strings"

	"jacred/core"
	"jacred/filedb"
)

var trailingReleaseYear = regexp.MustCompile(`^(.+?)\s+((?:19|20)\d{2})$`)

// searchReleases preserves explicit card fields and genuine numeric titles.
// A trailing year also searches the title/year pair: unrelated raw aliases
// (for example F1 2025 sports) must not suppress the requested film.
func (s *Server) searchReleases(p filedb.SearchParams) (filedb.JackettResult, error) {
	res, err := s.DB.JackettSearch(p)
	if err != nil || p.Title != "" || p.TitleOriginal != "" {
		return res, err
	}
	query := p.Query
	if stripped, _, _ := core.StripSeasonEpisode(query); len(res.Results) == 0 && stripped != "" {
		p.Query = stripped
		res, err = s.DB.JackettSearch(p)
		if err != nil || len(res.Results) > 0 {
			return res, err
		}
		query = stripped
	}
	if p.Year == 0 {
		if m := trailingReleaseYear.FindStringSubmatch(strings.TrimSpace(query)); m != nil {
			p.Query = strings.TrimSpace(m[1])
			p.Year, _ = strconv.Atoi(m[2])
			yearResults, yearErr := s.DB.JackettSearch(p)
			if yearErr != nil {
				return res, yearErr
			}
			// Keep successful numeric titles, deduplicating aliases by release URL.
			seen := make(map[string]bool, len(yearResults.Results))
			for _, row := range yearResults.Results {
				seen[asString(row["url"])] = true
			}
			for _, row := range res.Results {
				if key := asString(row["url"]); !seen[key] {
					yearResults.Results = append(yearResults.Results, row)
					seen[key] = true
				}
			}
			return yearResults, nil
		}
	}
	return res, err
}
