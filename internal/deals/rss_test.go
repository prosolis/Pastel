package deals

import "testing"

// A trimmed real DealNews Electronics item.
const dealNewsItemXML = `<rss><channel><item>
  <title>Amazon Early Prime Day Coupon Deals: Up to 49% off + free shipping</title>
  <link>https://www.dealnews.com/Amazon-Early-Prime-Day/21844660.html?iref=rss-c142</link>
  <description>&lt;div&gt;&lt;p&gt;Amazon's Coupon Deals page features savings up to 49% off ahead of Prime Day. Shop Now at Amazon&lt;/p&gt;&lt;/div&gt;</description>
  <guid>https://www.dealnews.com/21844660.html?iref=rss-c142</guid>
  <pubDate>Sat, 20 Jun 2026 08:26:53 -0400</pubDate>
</item></channel></rss>`

// A trimmed real Slickdeals frontpage item (2-digit year, store domain in
// brackets in the description).
const slickItemXML = `<rss><channel><item>
  <title><![CDATA[44-Pc RYOBI Essentials Impact Driving Set $12.90 + Free S&H]]></title>
  <link>https://slickdeals.net/f/19650567-44-piece-ryobi-set?utm_source=rss&amp;utm_medium=RSS2</link>
  <description><![CDATA[Home Depot [homedepot.com] has *44-Piece RYOBI Set* on sale for *$12.88*. *Shipping is free.*]]></description>
  <guid>thread-19650567</guid>
  <pubDate>Sat, 20 Jun 26 15:41:19 +0000</pubDate>
</item></channel></rss>`

func TestDealNewsItemParsing(t *testing.T) {
	feed, err := unmarshalRSS(dealNewsItemXML)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := dealNewsItem(feed.Items[0], "tech")
	if !ok {
		t.Fatal("expected a valid deal")
	}
	if d.Store != "Amazon" {
		t.Errorf("store: want Amazon, got %q", d.Store)
	}
	if d.Category != "tech" {
		t.Errorf("category: want tech, got %q", d.Category)
	}
	if d.Discount != 49 {
		t.Errorf("discount: want 49, got %d", d.Discount)
	}
	if d.DedupID != "dealnews-21844660" {
		t.Errorf("dedupID: want dealnews-21844660, got %q", d.DedupID)
	}
	if d.PostedAt.IsZero() || d.PostedAt.Year() != 2026 {
		t.Errorf("postedAt: want 2026, got %v", d.PostedAt)
	}
}

func TestSlickdealsItemParsing(t *testing.T) {
	feed, err := unmarshalRSS(slickItemXML)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := slickdealsItem(feed.Items[0])
	if !ok {
		t.Fatal("expected a valid deal")
	}
	if d.Store != "homedepot.com" {
		t.Errorf("store: want homedepot.com, got %q", d.Store)
	}
	if d.Price != 12.90 {
		t.Errorf("price: want 12.90 (from title), got %v", d.Price)
	}
	if d.Category != "general" {
		t.Errorf("category: want general, got %q", d.Category)
	}
	if d.DedupID != "slickdeals-19650567" {
		t.Errorf("dedupID: want slickdeals-19650567, got %q", d.DedupID)
	}
	// 2-digit-year pubDate must still parse.
	if d.PostedAt.IsZero() || d.PostedAt.Year() != 2026 {
		t.Errorf("postedAt: want 2026, got %v", d.PostedAt)
	}
	// utm tracking query must be stripped from the link.
	if got := d.URL; got != "https://slickdeals.net/f/19650567-44-piece-ryobi-set" {
		t.Errorf("url not cleaned: %q", got)
	}
}
