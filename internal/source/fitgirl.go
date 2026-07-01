package source

import (
	"context"
	"net/http"
	"time"
)

type FitGirl struct {
	Client *http.Client
	Base   string
}

func NewFitGirl() *FitGirl {
	return &FitGirl{Client: &http.Client{Timeout: 20 * time.Second}, Base: "https://fitgirl-repacks.site"}
}

func (f *FitGirl) setHTTPClient(c *http.Client) { f.Client = c }
func (f *FitGirl) Name() string                 { return "FitGirl" }

func (f *FitGirl) Search(ctx context.Context, query string) ([]Result, error) {
	return fetchWordpressRSS(ctx, f.Base, "FitGirl", "games", query, f.Client)
}
