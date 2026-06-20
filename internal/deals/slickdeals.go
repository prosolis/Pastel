package deals

import (
	"regexp"
	"strings"
)

// slickdealsFeed is the frontpage ("popular") feed. Slickdeals items are not
// tagged with a product vertical (their <category> is just "Frontpage Deals"),
// so everything lands in the catch-all "general" category.
const (
	slickdealsFeedURL  = "https://feeds.feedburner.com/SlickdealsnetFP"
	slickdealsCategory = "general"
)

// Slickdeals descriptions embed the retailer's domain in brackets, e.g.
// "Home Depot [homedepot.com] has …" — the cleanest store signal available.
var reSlickStore = regexp.MustCompile(`\[([a-z0-9-]+(?:\.[a-z0-9-]+)+)\]`)

// FetchSlickdealsDeals scrapes the Slickdeals frontpage feed and returns the
// posts as normalized "general" deals.
func FetchSlickdealsDeals() ([]WebDeal, error) {
	rss, err := fetchRSS(slickdealsFeedURL)
	if err != nil {
		return nil, err
	}

	var out []WebDeal
	for _, item := range rss.Items {
		if d, valid := slickdealsItem(item); valid {
			out = append(out, d)
		}
	}
	return out, nil
}

func slickdealsItem(item rssItem) (WebDeal, bool) {
	title := strings.TrimSpace(item.Title)
	if title == "" || item.Link == "" {
		return WebDeal{}, false
	}
	desc := stripTags(item.Description)

	price, hasPrice := parsePrice(title)
	if !hasPrice {
		price, hasPrice = parsePrice(desc)
	}
	discount := parseDiscount(title)
	if discount == 0 {
		discount = parseDiscount(desc)
	}
	isFree := (hasPrice && price == 0) || (!hasPrice && mentionsFree(title)) || discount == 100

	store := slickdealsStore(desc)

	id := item.GUID
	if id == "" {
		id = item.Link
	}

	return WebDeal{
		Source:   "slickdeals",
		Category: slickdealsCategory,
		Title:    title,
		URL:      cleanSlickLink(item.Link),
		Store:    store,
		Price:    price,
		Discount: discount,
		IsFree:   isFree,
		PostedAt: parseRSSTime(item.PubDate),
		DedupID:  "slickdeals-" + strings.TrimPrefix(id, "thread-"),
	}, true
}

// slickdealsStore reads the retailer domain from the "[domain.com]" marker,
// falling back to the leading text of the description (the store display name).
func slickdealsStore(desc string) string {
	if m := reSlickStore.FindStringSubmatch(desc); m != nil {
		return strings.ToLower(m[1])
	}
	if i := strings.Index(desc, " ["); i > 0 {
		return strings.TrimSpace(desc[:i])
	}
	return ""
}

// cleanSlickLink drops the RSS tracking query (utm_source=rss&…) from a
// Slickdeals thread permalink.
func cleanSlickLink(link string) string {
	if i := strings.Index(link, "?utm_"); i >= 0 {
		return link[:i]
	}
	return link
}
