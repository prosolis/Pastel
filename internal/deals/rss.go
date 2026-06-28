package deals

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WebDeal is a deal scraped from an RSS deal aggregator (DealNews, Slickdeals,
// …), normalized into the shape persist.go maps onto database.Deal. These are
// web-UI-only: unlike the game sources they are never posted to Matrix. Prices
// and discounts are best-effort parsed out of the title/description, since RSS
// has no structured price field.
type WebDeal struct {
	Source   string // "dealnews", "slickdeals", …
	Category string // "tech", "clothing", "general", …
	Title    string
	URL      string  // the deal page (aggregator permalink or outbound link)
	Store    string  // retailer, e.g. "amazon.com" or "Home Depot"
	Price    float64 // 0 when none could be parsed
	Discount int     // percent off parsed from the text, else 0
	IsFree   bool
	ImageURL string // thumbnail extracted from media/enclosure/<img>, "" when none
	PostedAt time.Time
	DedupID  string
}

// rssFeed/rssItem model the subset of RSS 2.0 we consume. Namespaced elements
// (content:encoded, dc:creator) are matched by local name and ignored unless
// listed here.
type rssFeed struct {
	XMLName xml.Name  `xml:"rss"`
	Items   []rssItem `xml:"channel>item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Category    string `xml:"category"`
	// Image carriers, matched by local name (namespace ignored): <media:content>
	// and <media:thumbnail> (Media RSS), <enclosure> (RSS core), and
	// <content:encoded> (the full HTML body, scanned for an <img> as a last resort).
	MediaContent   []rssMedia     `xml:"content"`
	MediaThumbnail []rssMedia     `xml:"thumbnail"`
	Enclosures     []rssEnclosure `xml:"enclosure"`
	ContentEncoded string         `xml:"encoded"`
}

// rssMedia models a <media:content>/<media:thumbnail> element; only the url and
// (optionally) type/medium attributes matter for picking an image.
type rssMedia struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Medium string `xml:"medium,attr"`
}

// rssEnclosure models an RSS <enclosure>; an image enclosure has a type like
// "image/jpeg".
type rssEnclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

const (
	rssUserAgent    = "Pastel/1.0 (deals aggregator; +https://deals.parodia.dev)"
	rssMaxRetries   = 3
	rssRetryBackoff = 3 * time.Second
)

var rssClient = &http.Client{Timeout: 20 * time.Second}

var (
	// First "$1,234.56" / "£99" / "€49,99" style price in some text.
	rePrice = regexp.MustCompile(`[$£€]\s?([0-9][0-9.,]*)`)
	// "50% off" / "50 % off" — the discount percentage.
	reDiscount = regexp.MustCompile(`(?i)([0-9]{1,3})\s?%\s*off`)
	// "up to 50% off" — a marketing ceiling across many items, not this deal's
	// actual discount, so parseDiscount ignores a percentage introduced this way.
	reUpToDiscount = regexp.MustCompile(`(?i)up\s+to\s+[0-9]{1,3}\s?%`)
	// Whole-word "free" (so "freedom" doesn't match).
	reFree = regexp.MustCompile(`(?i)\bfree\b`)
	// "free shipping" / "free returns" / "free trial" etc. — a free *perk*, not a
	// free *product*. Stripped before testing for a genuine freebie.
	reFreePerk = regexp.MustCompile(`(?i)free\s+(?:shipping|ship|s\s*&\s*h|s&h|returns?|delivery|in-store pickup|pickup|trial|gift card|gift)`)
	// Strips HTML tags so we can scan the plain text of a description.
	reTags = regexp.MustCompile(`<[^>]*>`)
	// First <img src="..."> in an HTML body, used as the last-resort image source.
	reImgSrc = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)
)

// imageFromItem extracts a thumbnail URL from an RSS item, preferring structured
// Media RSS/enclosure elements over scraping an <img> out of the HTML body.
// Returns "" when no http(s) image can be found, leaving the card image-less.
func imageFromItem(item rssItem) string {
	// 1. <media:content> / <media:thumbnail> with an image (or unspecified) type.
	for _, group := range [][]rssMedia{item.MediaContent, item.MediaThumbnail} {
		for _, m := range group {
			if isImageURL(m.URL, m.Type, m.Medium) {
				return m.URL
			}
		}
	}
	// 2. An <enclosure> declared as an image.
	for _, e := range item.Enclosures {
		if isImageURL(e.URL, e.Type, "") {
			return e.URL
		}
	}
	// 3. First <img> in the description or full content body.
	for _, body := range []string{item.Description, item.ContentEncoded} {
		if m := reImgSrc.FindStringSubmatch(body); m != nil {
			if u := strings.TrimSpace(html.UnescapeString(m[1])); isHTTPURL(u) {
				return u
			}
		}
	}
	return ""
}

// isImageURL reports whether a media/enclosure entry should be treated as an
// image: an http(s) URL whose declared type/medium is an image, or unspecified
// (many feeds omit the type on media:thumbnail).
func isImageURL(u, typ, medium string) bool {
	if !isHTTPURL(u) {
		return false
	}
	if typ == "" && medium == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(typ), "image") || strings.EqualFold(medium, "image")
}

// isHTTPURL reports whether u is a non-empty http(s) URL. The frontend re-guards
// the src, but filtering here keeps junk out of the DB.
func isHTTPURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// fetchRSS GETs an RSS feed and decodes it, retrying with a fixed backoff on
// transient failures. Aggregator feeds are not aggressively IP-throttled the way
// Reddit's are, so a simple retry is enough.
func fetchRSS(reqURL string) (*rssFeed, error) {
	var lastErr error
	for attempt := 0; attempt < rssMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(rssRetryBackoff)
		}
		req, err := http.NewRequest(http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("rss request build failed: %w", err)
		}
		req.Header.Set("User-Agent", rssUserAgent)

		resp, err := rssClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("rss %s returned status %d", reqURL, resp.StatusCode)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		feed, err := unmarshalRSS(string(body))
		if err != nil {
			return nil, fmt.Errorf("rss %s decode failed: %w", reqURL, err)
		}
		return feed, nil
	}
	return nil, lastErr
}

// unmarshalRSS decodes an RSS 2.0 document into rssFeed.
func unmarshalRSS(body string) (*rssFeed, error) {
	var feed rssFeed
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		return nil, err
	}
	return &feed, nil
}

// stripTags removes HTML tags and collapses whitespace, yielding the plain text
// of an RSS description.
func stripTags(s string) string {
	return strings.Join(strings.Fields(reTags.ReplaceAllString(s, " ")), " ")
}

// parseDealText extracts the price, discount, and free-ness from an RSS item's
// title and description using the best-effort heuristics shared by every RSS
// source. Price and discount are read from the title first, then the body.
func parseDealText(title, desc string) (price float64, hasPrice bool, discount int, isFree bool) {
	price, hasPrice = parsePrice(title)
	if !hasPrice {
		price, hasPrice = parsePrice(desc)
	}
	discount = parseDiscount(title)
	if discount == 0 {
		discount = parseDiscount(desc)
	}
	// A parsed $0, or a genuine "free" mention, makes the product itself free.
	// A 100% discount is deliberately NOT treated as free: aggregator headlines
	// say "up to 100% off" as marketing copy (already zeroed by parseDiscount's
	// up-to guard), which would otherwise flag whole feeds as giveaways.
	isFree = (hasPrice && price == 0) || (!hasPrice && mentionsFree(title))
	return price, hasPrice, discount, isFree
}

// parsePrice pulls the first currency amount out of some text. The bool reports
// whether any price token was present at all, so a genuine "0" is distinguishable
// from "no price mentioned". Handles both "1,234.56" (US) grouping.
func parsePrice(text string) (float64, bool) {
	m := rePrice.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	clean := strings.ReplaceAll(m[1], ",", "")
	clean = strings.TrimRight(clean, ".")
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// mentionsFree reports whether text advertises a genuinely free item, ignoring
// "free shipping" and similar perks that don't make the product itself free.
func mentionsFree(text string) bool {
	return reFree.MatchString(reFreePerk.ReplaceAllString(text, ""))
}

// parseDiscount pulls a "NN% off" percentage out of some text, clamped to 100.
// "Up to NN% off" marketing copy is ignored — it is a ceiling across a feed, not
// this deal's real discount.
func parseDiscount(text string) int {
	if reUpToDiscount.MatchString(text) {
		return 0
	}
	m := reDiscount.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v < 0 {
		return 0
	}
	if v > 100 {
		v = 100
	}
	return v
}

// rssTimeLayouts covers the pubDate spellings aggregators emit: 4-digit year
// (DealNews) and 2-digit year (Slickdeals), with numeric or named zones.
var rssTimeLayouts = []string{
	time.RFC1123Z,                   // Mon, 02 Jan 2006 15:04:05 -0700
	time.RFC1123,                    // Mon, 02 Jan 2006 15:04:05 MST
	"Mon, 02 Jan 06 15:04:05 -0700", // 2-digit year, numeric zone
	"Mon, 02 Jan 06 15:04:05 MST",   // 2-digit year, named zone
}

// parseRSSTime parses an RSS pubDate, falling back to now if none of the known
// layouts match.
func parseRSSTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range rssTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}
