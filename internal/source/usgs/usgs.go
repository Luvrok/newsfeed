// Package usgs reads the official USGS earthquake GeoJSON feeds.
package usgs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"newsfeed/internal/model"
	"newsfeed/internal/source"
	"newsfeed/internal/source/httpx"
)

func init() {
	source.Register("usgs", func() source.Fetcher { return &fetcher{} })
}

// Options:
//
//	feed   = "2.5"   # significant | 4.5 | 2.5 | 1.0 | all
//	period = "day"   # hour | day | week
//	limit  = 30
func (f *fetcher) Fetch(ctx context.Context, opts source.Options) ([]model.Item, error) {
	feed := opts.String("feed", "2.5")
	switch feed {
	case "significant", "4.5", "2.5", "1.0", "all":
	default:
		feed = "2.5"
	}
	period := opts.String("period", "day")
	switch period {
	case "hour", "day", "week":
	default:
		period = "day"
	}
	limit := opts.Int("limit", 30)
	endpoint := fmt.Sprintf("https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/%s_%s.geojson", feed, period)

	client := httpx.Client(25*time.Second, "newsfeed/0.1 (+usgs)")
	var data featureCollection
	if _, err := httpx.GetJSON(ctx, client, endpoint, &data); err != nil {
		return nil, fmt.Errorf("usgs: %w", err)
	}

	out := make([]model.Item, 0, len(data.Features))
	for _, f := range data.Features {
		p := f.Properties
		if p.Title == "" || p.URL == "" {
			continue
		}
		if p.Type != "" && p.Type != "earthquake" {
			continue
		}
		depth := 0.0
		if len(f.Geometry.Coordinates) >= 3 {
			depth = f.Geometry.Coordinates[2]
		}
		tags := []string{}
		if p.Mag != nil {
			tags = append(tags, fmt.Sprintf("magnitude %.1f", *p.Mag))
		}
		if p.Tsunami > 0 {
			tags = append(tags, "tsunami warning")
		}
		summary := ""
		if p.Place != "" {
			summary = p.Place
		}
		if depth != 0 {
			summary = strings.TrimSpace(fmt.Sprintf("%s\nDepth %.1f km", summary, depth))
		}
		out = append(out, model.Item{
			ExternalID: f.ID,
			URL:        p.URL,
			Title:      p.Title,
			Summary:    summary,
			Published:  time.UnixMilli(p.Time),
			Tags:       tags,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type featureCollection struct {
	Features []struct {
		ID         string `json:"id"`
		Properties struct {
			Mag     *float64 `json:"mag"`
			Place   string   `json:"place"`
			Time    int64    `json:"time"`
			URL     string   `json:"url"`
			Title   string   `json:"title"`
			Type    string   `json:"type"`
			Tsunami int      `json:"tsunami"`
		} `json:"properties"`
		Geometry struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

type fetcher struct{}
