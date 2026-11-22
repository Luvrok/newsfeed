// Package all imports every source implementation for its registration side
// effect. Adding a source is one import line here.
package all

import (
	_ "newsfeed/internal/source/gdelt"
	_ "newsfeed/internal/source/githubreleases"
	_ "newsfeed/internal/source/hackernews"
	_ "newsfeed/internal/source/lobsters"
	_ "newsfeed/internal/source/mastodon"
	_ "newsfeed/internal/source/rss"
	_ "newsfeed/internal/source/telegram"
	_ "newsfeed/internal/source/usgs"
)
