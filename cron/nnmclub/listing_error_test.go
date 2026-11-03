package nnmclub

import (
	"context"
	"jacred/app"
	"jacred/filedb"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListingFailureMustNotSettlePage(t *testing.T) {
	for _, status := range []int{403, 503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				w.Write([]byte("not a tracker listing"))
			}))
			defer s.Close()
			cfg := app.DefaultConfig()
			cfg.NNMClub.Host = s.URL
			cfg.NNMClub.Alias = ""
			cfg.NNMClub.ReqMinute = 0
			cfg.NNMClub.ParseDelay = 0
			dir := t.TempDir()
			p := New(cfg, filedb.New(cfg, dir), dir)
			if _, err := p.parsePage(context.Background(), "1", 0); err == nil {
				t.Fatal("bad page treated as completed empty listing")
			}
		})
	}
}
