package formatter

import (
	"fmt"
	"html"
	"math"
	"strings"

	"github.com/prosolis/Pastel/internal/currency"
	"github.com/prosolis/Pastel/internal/deals"
)

// Message holds both plain-text and HTML versions of a formatted message.
type Message struct {
	Plain string
	HTML  string
}

// FormatCheapSharkDeal formats a CheapShark deal for Matrix.
func FormatCheapSharkDeal(d deals.CheapSharkDeal, conv *currency.Converter) Message {
	discount := int(math.Floor(d.Savings))
	saleMulti := conv.FormatPrice(d.SalePrice)
	normalMulti := conv.FormatPrice(d.NormalPrice)

	var plain, htmlB strings.Builder

	plain.WriteString(fmt.Sprintf("🎮 [DEAL] %s\n", d.Title))
	plain.WriteString(fmt.Sprintf("  %d%% off on %s (was %s)\n", discount, d.StoreName, normalMulti))
	plain.WriteString(fmt.Sprintf("  💰 %s\n", saleMulti))

	htmlB.WriteString(fmt.Sprintf("<strong>🎮 [DEAL] %s</strong><br>\n", html.EscapeString(d.Title)))
	htmlB.WriteString(fmt.Sprintf("%d%% off on %s <del>%s</del><br>\n",
		discount, html.EscapeString(d.StoreName), html.EscapeString(normalMulti)))
	htmlB.WriteString(fmt.Sprintf("💰 <strong>%s</strong><br>\n", html.EscapeString(saleMulti)))

	if d.IsHistLow {
		plain.WriteString("  🏆 All-time low!\n")
		htmlB.WriteString("🏆 <em>All-time low!</em><br>\n")
	}

	plain.WriteString(fmt.Sprintf("  🔗 %s", d.DealURL))
	htmlB.WriteString(fmt.Sprintf("🔗 <a href=\"%s\">View Deal</a>", html.EscapeString(d.DealURL)))

	return Message{Plain: plain.String(), HTML: htmlB.String()}
}

// FormatITADDeal formats an ITAD deal for Matrix.
func FormatITADDeal(d deals.ITADDeal, conv *currency.Converter) Message {
	saleMulti := conv.FormatPrice(d.Price)
	normalMulti := conv.FormatPrice(d.Regular)

	var plain, htmlB strings.Builder

	plain.WriteString(fmt.Sprintf("🎮 [DEAL] %s\n", d.Title))
	plain.WriteString(fmt.Sprintf("  %d%% off on %s (was %s)\n", d.Discount, d.ShopName, normalMulti))
	plain.WriteString(fmt.Sprintf("  💰 %s\n", saleMulti))

	htmlB.WriteString(fmt.Sprintf("<strong>🎮 [DEAL] %s</strong><br>\n", html.EscapeString(d.Title)))
	htmlB.WriteString(fmt.Sprintf("%d%% off on %s <del>%s</del><br>\n",
		d.Discount, html.EscapeString(d.ShopName), html.EscapeString(normalMulti)))
	htmlB.WriteString(fmt.Sprintf("💰 <strong>%s</strong><br>\n", html.EscapeString(saleMulti)))

	if d.IsHistLow {
		plain.WriteString("  🏆 All-time low!\n")
		htmlB.WriteString("🏆 <em>All-time low!</em><br>\n")
	}

	plain.WriteString(fmt.Sprintf("  🔗 %s", d.URL))
	htmlB.WriteString(fmt.Sprintf("🔗 <a href=\"%s\">View Deal</a>", html.EscapeString(d.URL)))

	return Message{Plain: plain.String(), HTML: htmlB.String()}
}

// FormatEpicFreeGame formats an Epic free game for Matrix.
func FormatEpicFreeGame(g deals.EpicFreeGame) Message {
	var plain, htmlB strings.Builder

	if g.Upcoming {
		plain.WriteString(fmt.Sprintf("📢 [UPCOMING FREE] %s\n", g.Title))
		plain.WriteString("  Coming soon — Free on Epic Games Store\n")

		htmlB.WriteString(fmt.Sprintf("<strong>📢 [UPCOMING FREE] %s</strong><br>\n", html.EscapeString(g.Title)))
		htmlB.WriteString("Coming soon — Free on Epic Games Store<br>\n")
	} else {
		plain.WriteString(fmt.Sprintf("🆓 [FREE] %s\n", g.Title))
		plain.WriteString("  Free on Epic Games Store\n")

		htmlB.WriteString(fmt.Sprintf("<strong>🆓 [FREE] %s</strong><br>\n", html.EscapeString(g.Title)))
		htmlB.WriteString("Free on Epic Games Store<br>\n")
	}

	if g.EndDate != nil {
		dateStr := g.EndDate.Format("January 2")
		plain.WriteString(fmt.Sprintf("  📅 Free until %s\n", dateStr))
		htmlB.WriteString(fmt.Sprintf("📅 <em>Free until %s</em><br>\n", html.EscapeString(dateStr)))
	}

	linkText := "Claim Now"
	if g.Upcoming {
		linkText = "Store Page"
	}

	plain.WriteString(fmt.Sprintf("  🔗 %s", g.URL))
	htmlB.WriteString(fmt.Sprintf("🔗 <a href=\"%s\">%s</a>", html.EscapeString(g.URL), linkText))

	return Message{Plain: plain.String(), HTML: htmlB.String()}
}

// FormatWatchlistNotification formats a DM notification for a watchlist match.
func FormatWatchlistNotification(watchedName, dealTitle, dealURL string, price string, discount int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔔 Deal alert for \"%s\"!\n", watchedName))
	sb.WriteString(fmt.Sprintf("  %s — %d%% off\n", dealTitle, discount))
	sb.WriteString(fmt.Sprintf("  💰 %s\n", price))
	sb.WriteString(fmt.Sprintf("  🔗 %s", dealURL))
	return sb.String()
}

// FormatWatchlistFreeNotification formats a DM notification for a free game watchlist match.
func FormatWatchlistFreeNotification(watchedName, dealTitle, dealURL string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔔 Free game alert for \"%s\"!\n", watchedName))
	sb.WriteString(fmt.Sprintf("  %s — Free on Epic Games Store\n", dealTitle))
	sb.WriteString(fmt.Sprintf("  🔗 %s", dealURL))
	return sb.String()
}
