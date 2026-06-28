package watchlist

import "testing"

func TestParseWatch(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantLabel   string
		wantMax     float64
		wantMinDisc int
		wantCat     string
	}{
		{"plain", "elden ring", "elden ring", 0, 0, ""},
		{"under price", "elden ring under 30", "elden ring", 30, 0, ""},
		{"below price", "elden ring below 19.99", "elden ring", 19.99, 0, ""},
		{"under with dollar", "laptop under $500", "laptop", 500, 0, ""},
		{"less-than form", "monitor < 200", "monitor", 200, 0, ""},
		{"percent off", "laptop over 40% off", "laptop", 0, 40, ""},
		{"percent no over", "hoodie 50% off", "hoodie", 0, 50, ""},
		{"category prefix", "category:clothing nike", "nike", 0, 0, "clothing"},
		{"keyword prefix", "keyword:mechanical keyboard", "mechanical keyboard", 0, 0, ""},
		{"combined", "category:tech laptop under 500 over 30% off", "laptop", 500, 30, "tech"},
		{"trims and collapses", "  elden    ring   under 30 ", "elden ring", 30, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWatch(tt.in)
			if got.Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, tt.wantLabel)
			}
			if got.MaxPrice != tt.wantMax {
				t.Errorf("MaxPrice = %v, want %v", got.MaxPrice, tt.wantMax)
			}
			if got.MinDiscount != tt.wantMinDisc {
				t.Errorf("MinDiscount = %v, want %v", got.MinDiscount, tt.wantMinDisc)
			}
			if got.Category != tt.wantCat {
				t.Errorf("Category = %q, want %q", got.Category, tt.wantCat)
			}
			if got.Normalized != Normalize(tt.wantLabel) {
				t.Errorf("Normalized = %q, want %q", got.Normalized, Normalize(tt.wantLabel))
			}
		})
	}
}
