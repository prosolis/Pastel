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

// TestImageFromItem checks the image-source precedence: a structured
// media:content beats media:thumbnail beats enclosure beats a scraped <img>, a
// non-image enclosure is ignored in favor of the <img> fallback, an entity-
// encoded <img src> is decoded, and an item with no image yields "".
func TestImageFromItem(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want string
	}{
		{
			"media:content preferred",
			`<rss><channel><item>
			  <media:content url="https://cdn.example/wide.jpg" type="image/jpeg"/>
			  <media:thumbnail url="https://cdn.example/thumb.jpg"/>
			  <description>&lt;img src="https://cdn.example/body.jpg"&gt;</description>
			</item></channel></rss>`,
			"https://cdn.example/wide.jpg",
		},
		{
			"media:thumbnail when no media:content",
			`<rss><channel><item>
			  <media:thumbnail url="https://cdn.example/thumb.jpg"/>
			  <enclosure url="https://cdn.example/enc.jpg" type="image/jpeg"/>
			</item></channel></rss>`,
			"https://cdn.example/thumb.jpg",
		},
		{
			"image enclosure when no media",
			`<rss><channel><item>
			  <enclosure url="https://cdn.example/enc.jpg" type="image/jpeg"/>
			</item></channel></rss>`,
			"https://cdn.example/enc.jpg",
		},
		{
			"non-image enclosure ignored, img fallback used",
			`<rss><channel><item>
			  <enclosure url="https://cdn.example/track.mp3" type="audio/mpeg"/>
			  <description>&lt;p&gt;&lt;img src=&quot;https://cdn.example/body.png&quot;&gt;Deal!&lt;/p&gt;</description>
			</item></channel></rss>`,
			"https://cdn.example/body.png",
		},
		{
			"no image yields empty",
			`<rss><channel><item>
			  <description>Plain text deal, no image at all.</description>
			</item></channel></rss>`,
			"",
		},
		{
			"non-http img src rejected",
			`<rss><channel><item>
			  <description>&lt;img src="data:image/png;base64,AAAA"&gt;</description>
			</item></channel></rss>`,
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			feed, err := unmarshalRSS(tc.xml)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := imageFromItem(feed.Items[0]); got != tc.want {
				t.Errorf("imageFromItem = %q, want %q", got, tc.want)
			}
		})
	}
}

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
