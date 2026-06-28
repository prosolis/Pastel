package database

// Trust verdict buckets stored in deals.verdict and read by the web UI to
// render a trust badge. An empty verdict means Pastel has no price history yet
// for the product and therefore makes no claim.
const (
	VerdictAllTimeLow = "all-time-low"
	VerdictGood       = "good"
	VerdictMeh        = "meh"
	VerdictNone       = ""
)

// goodThreshold is how close (multiplicatively) the current price must be to the
// lowest ever observed to count as a "good" deal — within 10%.
const goodThreshold = 1.10

// minObsForATL is the number of *distinct* prior prices Pastel must have observed
// for a product before it will award the "all-time low" badge. Without this gate
// a product seen only once or twice could claim ATL off a near-empty history,
// which gutted the badge's meaning. A genuine new low on a thinly-observed
// product still reads as "good" until enough history accumulates to trust it.
const minObsForATL = 3

// suspectDiscountPct is the discount above which a non-game deal is treated as
// suspicious unless corroborated — retailers routinely inflate MSRP to advertise
// huge "savings".
const suspectDiscountPct = 70

// suspectInflationFactor flags a "normal" price that towers over what the item
// has actually sold for: if normal_price exceeds the median observed sale price
// by this factor, the advertised discount is likely against a fake MSRP.
const suspectInflationFactor = 3.0

// ComputeVerdict classifies a deal's current price against the lowest price
// Pastel has ever observed for it (low/haveLow) and ITAD's own historical-low
// flag (itadHistLow). priorObs is the number of distinct prices already in
// history (before this sighting). It returns the verdict bucket; an unknown/zero
// current price or absent history yields VerdictNone so the UI shows no badge
// rather than a misleading one.
//
// "all-time low" requires the current price to be *strictly* below the prior low
// (an unchanged price re-saved each fetch cycle no longer earns the badge) AND
// enough distinct prior observations to trust the low is meaningful. A new low on
// a thinly-observed product reads as "good" until its history fills in.
func ComputeVerdict(salePrice, low float64, haveLow bool, priorObs int, itadHistLow bool) string {
	if itadHistLow {
		return VerdictAllTimeLow
	}
	if !haveLow || salePrice <= 0 || low <= 0 {
		return VerdictNone
	}
	switch {
	case salePrice < low && priorObs >= minObsForATL:
		return VerdictAllTimeLow
	case salePrice <= low*goodThreshold:
		return VerdictGood
	default:
		return VerdictMeh
	}
}

// IsSuspectDiscount reports whether a deal's advertised discount looks inflated
// and should be shown with a "check the price" warning. Games are excluded
// because their discounts are computed against real storefront list prices.
// median/haveMedian come from observed price history (DB.MedianPrice).
func IsSuspectDiscount(category string, discount int, normalPrice, median float64, haveMedian bool) bool {
	if category == "games" {
		return false
	}
	if haveMedian && median > 0 && normalPrice > median*suspectInflationFactor {
		return true
	}
	return discount >= suspectDiscountPct
}

// SaveDealWithVerdict records the deal's price into the price-history table,
// computes its trust verdict + suspect-discount flag from the accumulated
// history, then upserts it. All web-facing save paths funnel through here so the
// verdict columns stay populated consistently across every source.
//
// The verdict is computed against PRIOR history (before recording the current
// price), so a product's first sighting makes no "all-time low" claim — it would
// otherwise compare equal to itself. A genuine new low on a later sighting, once
// enough distinct prices have been observed, then reads as all-time-low.
func (d *DB) SaveDealWithVerdict(deal Deal) error {
	key := PriceKey(deal.Category, deal.Store, deal.TitleNorm)

	low, haveLow := d.LowestPrice(key)
	median, haveMedian := d.MedianPrice(key)
	priorObs := d.DistinctPriceCount(key)
	deal.Verdict = ComputeVerdict(deal.SalePrice, low, haveLow, priorObs, bool(deal.IsHistLow))
	if haveLow {
		deal.PriceLow = low // lowest seen *before now*; drives "seen as low as"
	}
	deal.PriceSuspect = Bool(IsSuspectDiscount(deal.Category, deal.Discount, deal.NormalPrice, median, haveMedian))

	if err := d.RecordPrice(key, deal.Source, deal.SalePrice); err != nil {
		return err
	}
	return d.SaveDeal(deal)
}
