package deals

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// dealNewsConcurrency bounds how many vertical feeds are fetched at once. The
// feeds are independent, so fanning out collapses the worst-case scan time from
// the sum of every feed's timeout/retries down to roughly one feed's — while the
// cap keeps Pastel from opening a dozen simultaneous connections to one host.
const dealNewsConcurrency = 5

// dealNewsFeeds maps DealNews browse categories (served as RSS at
// dealnews.com/c<NN>/<Name>/?rss=1) onto Pastel's category dimension. Each feed
// is a vertical, and the category travels with every deal scraped from it.
//
// IMPORTANT: DealNews resolves a feed solely by its numeric c<NN> id and ignores
// the human-readable name in the path — request a wrong number and it silently
// 301s to whatever category that id really is (e.g. c1136 is *Generators*, not
// pets). The name segment below is therefore only documentation; the id is the
// contract. Every id here is verified to resolve to the named top-level category
// (don't reuse the wrong-numbers history: c238 is Automotive, NOT tools).
var dealNewsFeeds = []struct {
	path     string // the c<NN>/<Name> path segment
	category string
}{
	{"c142/Electronics", "tech"},
	{"c39/Computers", "tech"},
	{"c202/Clothing-Accessories", "clothing"},
	{"c186/Gaming-Toys", "games"},
	{"c196/Home-Garden", "home"},
	{"c211/Sports-Fitness", "sports"},
	{"c178/Movies-Music-Books", "media"},
	// Phase 5 coverage expansion — each is a distinct DealNews vertical (Slickdeals
	// RSS, by contrast, ignores its category params and only serves the frontpage,
	// so DealNews is the reliable way to add real verticals). DealNews has no pet
	// or baby/kids category at all, so those verticals are not sourced here.
	{"c197/Home-Garden/Tools-Hardware", "tools"},
	{"c756/Health-Beauty", "beauty"},
	{"c238/Automotive", "auto"},
	{"c182/Office-School-Supplies", "office"},
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
	type result struct {
		deals []WebDeal
		err   error
	}
	results := make([]result, len(dealNewsFeeds))

	var wg sync.WaitGroup
	sem := make(chan struct{}, dealNewsConcurrency)
	for i, feed := range dealNewsFeeds {
		wg.Add(1)
		go func(i int, feed struct {
			path     string
			category string
		}) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reqURL := fmt.Sprintf("https://www.dealnews.com/%s/?rss=1", feed.path)
			rss, err := fetchRSS(reqURL)
			if err != nil {
				results[i].err = err
				return
			}
			for _, item := range rss.Items {
				if d, valid := dealNewsItem(item, feed.category); valid {
					results[i].deals = append(results[i].deals, d)
				}
			}
		}(i, feed)
	}
	wg.Wait()

	// Merge in feed order so output stays deterministic regardless of which
	// goroutine finished first. Tolerate per-feed failures as long as one feed
	// succeeded; only surface an error if every feed failed.
	var out []WebDeal
	var lastErr error
	ok := false
	for _, r := range results {
		if r.err != nil {
			lastErr = r.err
			continue
		}
		ok = true
		out = append(out, r.deals...)
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
	price, _, discount, isFree := parseDealText(title, desc)

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
		ImageURL: imageFromItem(item),
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
