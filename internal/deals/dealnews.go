package deals

import (
	"fmt"
	"regexp"
	"strings"
)

// dealNewsFeeds maps DealNews browse categories (served as RSS at
// dealnews.com/c<NN>/<Name>/?rss=1) onto Pastel's category dimension. Each feed
// is a vertical, and the category travels with every deal scraped from it.
var dealNewsFeeds = []struct {
	path     string // the c<NN>/<Name> path segment
	category string
}{
	{"c142/Electronics", "tech"},
	{"c108/Computers", "tech"},
	{"c202/Clothing-Accessories", "clothing"},
	{"c186/Gaming-Toys", "games"},
	{"c1009/Home-Garden", "home"},
	{"c211/Sports-Fitness", "sports"},
	{"c178/Movies-Music-Books", "media"},
}

// DealNews descriptions carry a "Shop Now at <Store></p>" call to action, which
// is the most reliable place to read the retailer (the <link> is a dealnews.com
// redirect, so the URL host is useless). The store sits between "now at" and the
// closing paragraph tag — feature-bullet text can follow in a later paragraph,
// so we must stop at </p> rather than running to end-of-text.
var reDealNewsStore = regexp.MustCompile(`(?i)(?:shop|buy|order|get it) now at\s+(.+?)\s*</p>`)

// FetchDealNewsDeals scrapes each configured DealNews category feed and returns
// the posts as normalized deals tagged with the feed's category. A failure on
// one feed is tolerated as long as at least one succeeds.
func FetchDealNewsDeals() ([]WebDeal, error) {
	var out []WebDeal
	var lastErr error
	ok := false

	for _, feed := range dealNewsFeeds {
		reqURL := fmt.Sprintf("https://www.dealnews.com/%s/?rss=1", feed.path)
		rss, err := fetchRSS(reqURL)
		if err != nil {
			lastErr = err
			continue
		}
		ok = true
		for _, item := range rss.Items {
			if d, valid := dealNewsItem(item, feed.category); valid {
				out = append(out, d)
			}
		}
	}

	if !ok && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func dealNewsItem(item rssItem, category string) (WebDeal, bool) {
	title := strings.TrimSpace(item.Title)
	if title == "" || item.Link == "" {
		return WebDeal{}, false
	}
	desc := stripTags(item.Description)

	// Price/discount can appear in either the title or the description body.
	price, hasPrice := parsePrice(title)
	if !hasPrice {
		price, hasPrice = parsePrice(desc)
	}
	discount := parseDiscount(title)
	if discount == 0 {
		discount = parseDiscount(desc)
	}
	isFree := (hasPrice && price == 0) || (!hasPrice && mentionsFree(title)) || discount == 100

	store := dealNewsStore(item.Description, title)

	id := item.GUID
	if id == "" {
		id = item.Link
	}

	return WebDeal{
		Source:   "dealnews",
		Category: category,
		Title:    title,
		URL:      item.Link,
		Store:    store,
		Price:    price,
		Discount: discount,
		IsFree:   isFree,
		PostedAt: parseRSSTime(item.PubDate),
		DedupID:  "dealnews-" + dealNewsID(id),
	}, true
}

// dealNewsStore reads the retailer from the "Shop Now at <Store></p>" CTA in the
// raw (HTML) description, falling back to the leading word of the title (DealNews
// titles usually lead with the store, e.g. "Amazon Early Prime Day …").
func dealNewsStore(rawDesc, title string) string {
	if m := reDealNewsStore.FindStringSubmatch(rawDesc); m != nil {
		if s := strings.TrimRight(strings.TrimSpace(stripTags(m[1])), "."); s != "" {
			return s
		}
	}
	if fields := strings.Fields(title); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// dealNewsID reduces a guid/link like "https://www.dealnews.com/21844660.html"
// to its stable numeric article id when possible, else returns the raw string.
func dealNewsID(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, ".?"); i >= 0 {
		s = s[:i]
	}
	return s
}
