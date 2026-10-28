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
// Only an unsuccessful free-text lookup retries a title with a trailing year.
func (s *Server) searchReleases(p filedb.SearchParams) (filedb.JackettResult, error) {
	res, err := s.DB.JackettSearch(p)
	if err != nil || len(res.Results) > 0 || p.Title != "" || p.TitleOriginal != "" {
		return res, err
	}
	query := p.Query
	if stripped, _, _ := core.StripSeasonEpisode(query); stripped != "" {
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
			return s.DB.JackettSearch(p)
		}
	}
	return res, err
}
