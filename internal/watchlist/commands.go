package watchlist

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/id"

	"github.com/prosolis/Pastel/internal/currency"
	"github.com/prosolis/Pastel/internal/deals"
)

const (
	searchRateLimit  = 5                // max searches per window
	searchRateWindow = 10 * time.Minute // sliding window
)

// DMSender is the interface for sending DMs to users.
type DMSender interface {
	SendDM(userID id.UserID, text string) error
}

// CommandHandler handles watchlist commands received via DM.
type CommandHandler struct {
	store  *Store
	sender DMSender
	conv   *currency.Converter

	rateMu    sync.Mutex
	rateLimit map[id.UserID][]time.Time // search timestamps per user
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(store *Store, sender DMSender, conv *currency.Converter) *CommandHandler {
	return &CommandHandler{
		store:     store,
		sender:    sender,
		conv:      conv,
		rateLimit: make(map[id.UserID][]time.Time),
	}
}

// checkSearchRate returns true if the user is within the rate limit.
func (h *CommandHandler) checkSearchRate(userID id.UserID) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-searchRateWindow)

	// Prune old entries
	recent := h.rateLimit[userID]
	filtered := recent[:0]
	for _, t := range recent {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) >= searchRateLimit {
		h.rateLimit[userID] = filtered
		return false
	}

	h.rateLimit[userID] = append(filtered, now)
	return true
}

// HandleMessage parses and dispatches a DM command.
func (h *CommandHandler) HandleMessage(senderID, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}

	var cmd, args string
	if idx := strings.IndexByte(body, ' '); idx > 0 {
		cmd = strings.ToLower(body[:idx])
		args = strings.TrimSpace(body[idx+1:])
	} else {
		cmd = strings.ToLower(body)
	}

	uid := id.UserID(senderID)

	switch cmd {
	case "!watch":
		h.handleWatch(uid, args)
	case "!unwatch":
		h.handleUnwatch(uid, args)
	case "!extend":
		h.handleExtend(uid, args)
	case "!search":
		h.handleSearch(uid, args)
	case "!watchlist":
		h.handleList(uid)
	case "!help":
		h.handleHelp(uid)
	}
}

func (h *CommandHandler) handleWatch(userID id.UserID, gameName string) {
	if gameName == "" {
		h.reply(userID, "Usage: !watch <game name>")
		return
	}

	added, err := h.store.AddWatch(string(userID), gameName)
	if err != nil {
		slog.Error("watchlist: add failed", "user", userID, "game", gameName, "error", err)
		h.reply(userID, "Something went wrong. Please try again.")
		return
	}

	if !added {
		h.reply(userID, fmt.Sprintf("You're already watching \"%s\".", gameName))
		return
	}

	expires := time.Now().Add(watchDuration)
	h.reply(userID, fmt.Sprintf("Added \"%s\" to your watchlist. I'll DM you when a deal appears. Expires %s.",
		gameName, expires.Format("January 2, 2006")))
}

func (h *CommandHandler) handleUnwatch(userID id.UserID, args string) {
	if args == "" {
		h.reply(userID, "Usage: !unwatch <# or game name>")
		return
	}

	gameName, err := h.resolveGameArg(userID, args)
	if err != nil {
		h.reply(userID, err.Error())
		return
	}

	removed, err := h.store.RemoveWatch(string(userID), gameName)
	if err != nil {
		slog.Error("watchlist: remove failed", "user", userID, "game", gameName, "error", err)
		h.reply(userID, "Something went wrong. Please try again.")
		return
	}

	if !removed {
		h.reply(userID, fmt.Sprintf("No watch found for \"%s\".", gameName))
		return
	}

	h.reply(userID, fmt.Sprintf("Removed \"%s\" from your watchlist.", gameName))
}

func (h *CommandHandler) handleExtend(userID id.UserID, args string) {
	if args == "" {
		h.reply(userID, "Usage: !extend <# or game name>")
		return
	}

	gameName, err := h.resolveGameArg(userID, args)
	if err != nil {
		h.reply(userID, err.Error())
		return
	}

	extended, err := h.store.ExtendWatch(string(userID), gameName)
	if err != nil {
		slog.Error("watchlist: extend failed", "user", userID, "game", gameName, "error", err)
		h.reply(userID, "Something went wrong. Please try again.")
		return
	}

	if !extended {
		h.reply(userID, fmt.Sprintf("No watch found for \"%s\".", gameName))
		return
	}

	expires := time.Now().Add(watchDuration)
	h.reply(userID, fmt.Sprintf("Extended \"%s\" — now expires %s.",
		gameName, expires.Format("January 2, 2006")))
}

func (h *CommandHandler) handleList(userID id.UserID) {
	entries, err := h.store.ListWatches(string(userID))
	if err != nil {
		slog.Error("watchlist: list failed", "user", userID, "error", err)
		h.reply(userID, "Something went wrong. Please try again.")
		return
	}

	if len(entries) == 0 {
		h.reply(userID, "Your watchlist is empty. Use !watch <game name> to add one.")
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Your watchlist (%d):\n", len(entries)))
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("  %d. %s (expires %s)\n", i+1, e.GameName, e.ExpiresAt.Format("January 2, 2006")))
	}
	sb.WriteString("\nUse !extend <#> or !unwatch <#> with the number above.")

	h.reply(userID, sb.String())
}

func (h *CommandHandler) handleSearch(userID id.UserID, query string) {
	if query == "" {
		h.reply(userID, "Usage: !search <game name>")
		return
	}

	if !h.checkSearchRate(userID) {
		h.reply(userID, fmt.Sprintf("Rate limited — max %d searches per %d minutes. Try again shortly.",
			searchRateLimit, int(searchRateWindow.Minutes())))
		return
	}

	h.conv.EnsureRates()

	results, err := deals.SearchCheapSharkDeals(query, 5)
	if err != nil {
		slog.Error("search: cheapshark failed", "query", query, "error", err)
		h.reply(userID, "Search failed. Please try again later.")
		return
	}

	if len(results) == 0 {
		h.reply(userID, fmt.Sprintf("No current deals found for \"%s\".", query))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Deals matching \"%s\":\n\n", query))
	for _, d := range results {
		discount := int(math.Floor(d.Savings))
		price := h.conv.FormatPrice(d.SalePrice)
		sb.WriteString(fmt.Sprintf("  %s\n", d.Title))
		sb.WriteString(fmt.Sprintf("    %d%% off on %s — %s\n", discount, d.StoreName, price))
		sb.WriteString(fmt.Sprintf("    %s\n\n", d.DealURL))
	}

	h.reply(userID, sb.String())
}

func (h *CommandHandler) handleHelp(userID id.UserID) {
	h.reply(userID, "Commands:\n"+
		"  !search <game name> — Search for current deals\n"+
		"  !watch <game name> — Watch for deals on a game\n"+
		"  !unwatch <# or game name> — Remove a game from your watchlist\n"+
		"  !extend <# or game name> — Reset the 180-day expiry timer\n"+
		"  !watchlist — Show your numbered watchlist\n"+
		"  !help — Show this message\n\n"+
		"Watches expire after 180 days. You'll get a reminder 7 days before.")
}

// resolveGameArg resolves an argument that is either a list number (from !watchlist)
// or a game name. Returns the game name.
func (h *CommandHandler) resolveGameArg(userID id.UserID, arg string) (string, error) {
	num, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil {
		return arg, nil // not a number, treat as game name
	}

	entries, err := h.store.ListWatches(string(userID))
	if err != nil {
		return "", fmt.Errorf("Something went wrong. Please try again.")
	}

	if num < 1 || num > len(entries) {
		return "", fmt.Errorf("Invalid number. Use !watchlist to see your list (1-%d).", len(entries))
	}

	return entries[num-1].GameName, nil
}

func (h *CommandHandler) reply(userID id.UserID, text string) {
	if err := h.sender.SendDM(userID, text); err != nil {
		slog.Error("watchlist: failed to send DM", "user", userID, "error", err)
	}
}
