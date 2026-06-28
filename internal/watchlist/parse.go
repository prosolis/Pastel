package watchlist

import (
	"regexp"
	"strconv"
	"strings"
)

// WatchSpec is a parsed !watch request: the thing to match plus optional
// predicate (price/discount) and category constraints.
type WatchSpec struct {
	Label       string  // display name shown back to the user
	Normalized  string  // normalized label used for substring matching
	MaxPrice    float64 // only match deals at or below this USD price (0 = any)
	MinDiscount int     // only match deals at or above this percent off (0 = any)
	Category    string  // only match deals in this category ("" = any)
}

var (
	// category:<word> anywhere in the input.
	reCategory = regexp.MustCompile(`(?i)\bcategory:([a-z0-9]+)`)
	// "under 30", "below 30", "< 30", optionally with a leading $.
	rePrice = regexp.MustCompile(`(?i)\b(?:under|below)\s+\$?(\d+(?:\.\d+)?)\b|<\s*\$?(\d+(?:\.\d+)?)`)
	// "40% off", "over 40% off", "over 40%".
	reDiscount = regexp.MustCompile(`(?i)\b(?:over\s+)?(\d{1,3})%(?:\s+off)?`)
	// leading keyword: prefix (just a hint; the remainder is the keyword).
	reKeywordPrefix = regexp.MustCompile(`(?i)^keyword:\s*`)
	reSpaces        = regexp.MustCompile(`\s+`)
)

// ParseWatch extracts predicate/category constraints from a !watch argument and
// returns the remaining text as the label to match on. It is forgiving: any
// token it doesn't recognize stays part of the label.
func ParseWatch(args string) WatchSpec {
	var spec WatchSpec
	s := args

	if m := reCategory.FindStringSubmatch(s); m != nil {
		spec.Category = strings.ToLower(m[1])
		s = reCategory.ReplaceAllString(s, " ")
	}

	if m := rePrice.FindStringSubmatch(s); m != nil {
		num := m[1]
		if num == "" {
			num = m[2] // the "< N" alternative
		}
		if v, err := strconv.ParseFloat(num, 64); err == nil {
			spec.MaxPrice = v
		}
		s = rePrice.ReplaceAllString(s, " ")
	}

	if m := reDiscount.FindStringSubmatch(s); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			spec.MinDiscount = v
		}
		s = reDiscount.ReplaceAllString(s, " ")
	}

	s = reKeywordPrefix.ReplaceAllString(s, "")
	s = strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))

	spec.Label = s
	spec.Normalized = Normalize(s)
	return spec
}
