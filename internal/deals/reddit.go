package deals

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RedditFeed maps a deal-community subreddit to the category its posts belong
// to. This is what turns Reddit into a multi-category source: each subreddit is
// a vertical (music gear, clothing, tech, …) and the category travels with
// every deal scraped from it.
type RedditFeed struct {
	Subreddit string // e.g. "buildapcsales" (no "r/" prefix)
	Category  string // e.g. "tech", "music", "clothing"
}

// RedditDeal is a deal scraped from a subreddit feed, normalized into the shape
// persist.go maps onto database.Deal. Prices/discounts are best-effort parsed
// out of the post title, since Reddit has no structured price field.
type RedditDeal struct {
	Title    string
	URL      string // outbound deal link (the retailer URL, not the comments page)
	Store    string // retailer host, e.g. "guitarcenter.com"
	Category string
	Price    float64 // 0 when none could be parsed from the title
	Discount int     // percent off parsed from the title, else 0
	IsFree   bool
	PostedAt time.Time
	DedupID  string
}

// Reddit blocks the unauthenticated `.json` API (403) but still serves Atom
// feeds at `/r/<sub>.rss`. We parse those instead. The feed entry's <content>
// is HTML holding two links — the outbound retailer URL labelled "[link]" and
// the Reddit comments page — plus the title, post id, and timestamp.
type redditAtom struct {
	XMLName xml.Name      `xml:"feed"`
	Entries []redditEntry `xml:"entry"`
}

type redditEntry struct {
	ID        string `xml:"id"`    // "t3_<id>"
	Title     string `xml:"title"` // post title (entity-decoded by the XML reader)
	Content   string `xml:"content"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
}

// Reddit asks API clients to send a unique, descriptive User-Agent and rate
// limits unauthenticated feed access hard (≈1 request per few seconds per IP,
// 429 with an x-ratelimit-reset hint). We send a real UA, space requests out,
// and back off on 429.
const (
	redditUserAgent  = "Pastel/1.0 (deals aggregator; +https://deals.parodia.dev)"
	redditFeedDelay  = 3 * time.Second // polite spacing between subreddits
	redditMaxRetries = 3
)

var redditClient = &http.Client{Timeout: 20 * time.Second}

var (
	// First "$1,234.56" style price in a title.
	rePrice = regexp.MustCompile(`\$\s?([0-9][0-9,]*(?:\.[0-9]{1,2})?)`)
	// "50% off" / "50 % off" — the discount percentage.
	reDiscount = regexp.MustCompile(`(?i)([0-9]{1,3})\s?%\s*off`)
	// Whole-word "free" (so "freedom" doesn't match).
	reFree = regexp.MustCompile(`(?i)\bfree\b`)
	// The outbound retailer link inside an entry's HTML content, labelled
	// "[link]" (the other anchor is "[comments]", pointing back to Reddit).
	reOutbound = regexp.MustCompile(`href="([^"]+)"\s*>\s*\[link\]`)
)

// FetchRedditDeals scrapes the newest posts from each configured feed and
// returns them as normalized deals tagged with the feed's category. Feeds are
// fetched sequentially with polite spacing to respect Reddit's rate limit; a
// failure on one subreddit is tolerated as long as at least one succeeds.
func FetchRedditDeals(feeds []RedditFeed) ([]RedditDeal, error) {
	var out []RedditDeal
	var lastErr error
	ok := false

	for i, feed := range feeds {
		if i > 0 {
			time.Sleep(redditFeedDelay)
		}
		deals, err := fetchSubreddit(feed)
		if err != nil {
			lastErr = err
			continue
		}
		ok = true
		out = append(out, deals...)
	}

	// Only surface an error if nothing at all came back; one dead or
	// rate-limited subreddit should not sink the others.
	if !ok && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func fetchSubreddit(feed RedditFeed) ([]RedditDeal, error) {
	reqURL := fmt.Sprintf("https://www.reddit.com/r/%s.rss", url.PathEscape(feed.Subreddit))

	body, err := redditGet(reqURL, feed.Subreddit)
	if err != nil {
		return nil, err
	}

	var atom redditAtom
	if err := xml.Unmarshal(body, &atom); err != nil {
		return nil, fmt.Errorf("reddit r/%s decode failed: %w", feed.Subreddit, err)
	}

	var deals []RedditDeal
	for _, e := range atom.Entries {
		outbound := extractOutbound(e.Content)
		store := storeFromURL(outbound)
		// No "[link]" anchor (self/text post) or a link that just points back to
		// Reddit (crosspost/announcement) is not a purchasable deal — skip it.
		if outbound == "" || store == "" || strings.HasSuffix(store, "reddit.com") {
			continue
		}
		title := strings.TrimSpace(e.Title)
		if title == "" {
			continue
		}

		price, hasPrice := parsePrice(title)
		discount := parseDiscount(title)
		isFree := (hasPrice && price == 0) || (!hasPrice && reFree.MatchString(title)) || discount == 100

		deals = append(deals, RedditDeal{
			Title:    title,
			URL:      outbound,
			Store:    store,
			Category: feed.Category,
			Price:    price,
			Discount: discount,
			IsFree:   isFree,
			PostedAt: parseAtomTime(e.Published, e.Updated),
			DedupID:  "reddit-" + strings.TrimPrefix(e.ID, "t3_"),
		})
	}
	return deals, nil
}

// redditGet fetches a feed URL, retrying with backoff when Reddit returns 429.
// It honours the x-ratelimit-reset hint when present.
func redditGet(reqURL, sub string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < redditMaxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("reddit request build failed: %w", err)
		}
		req.Header.Set("User-Agent", redditUserAgent)

		resp, err := redditClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("reddit request failed for r/%s: %w", sub, err)
			time.Sleep(redditFeedDelay)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := resetWait(resp.Header.Get("x-ratelimit-reset"))
			resp.Body.Close()
			lastErr = fmt.Errorf("reddit r/%s rate limited (429)", sub)
			time.Sleep(wait)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("reddit r/%s returned status %d", sub, resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reddit r/%s read failed: %w", sub, err)
		}
		return body, nil
	}
	return nil, lastErr
}

// resetWait converts an x-ratelimit-reset header (seconds until the window
// resets) into a sleep duration, with a sane fallback and clamp.
func resetWait(header string) time.Duration {
	secs, err := strconv.ParseFloat(strings.TrimSpace(header), 64)
	if err != nil || secs <= 0 {
		return redditFeedDelay * 2
	}
	if secs > 30 {
		secs = 30
	}
	return time.Duration(secs+1) * time.Second
}

// extractOutbound pulls the retailer URL out of an entry's HTML content.
func extractOutbound(content string) string {
	m := reOutbound.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// storeFromURL returns the retailer host without a leading "www." (e.g.
// "https://www.guitarcenter.com/x" -> "guitarcenter.com"). Empty on parse error.
func storeFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

// parseAtomTime parses an entry timestamp, preferring published over updated,
// falling back to now if neither parses.
func parseAtomTime(published, updated string) time.Time {
	for _, s := range []string{published, updated} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// parsePrice pulls the first dollar amount out of a title. The bool reports
// whether any price token was present at all, so "$0" (genuinely free) is
// distinguishable from "no price mentioned".
func parsePrice(title string) (float64, bool) {
	m := rePrice.FindStringSubmatch(title)
	if m == nil {
		return 0, false
	}
	clean := strings.ReplaceAll(m[1], ",", "")
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseDiscount pulls a "NN% off" percentage out of a title, clamped to 100.
func parseDiscount(title string) int {
	m := reDiscount.FindStringSubmatch(title)
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
