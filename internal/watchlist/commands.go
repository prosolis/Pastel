package watchlist

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"maunium.net/go/mautrix/id"
)

// DMSender is the interface for sending DMs to users.
type DMSender interface {
	SendDM(userID id.UserID, text string) error
}

// CommandHandler handles watchlist commands received via DM.
type CommandHandler struct {
	store  *Store
	sender DMSender
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(store *Store, sender DMSender) *CommandHandler {
	return &CommandHandler{store: store, sender: sender}
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

func (h *CommandHandler) handleUnwatch(userID id.UserID, gameName string) {
	if gameName == "" {
		h.reply(userID, "Usage: !unwatch <game name>")
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

func (h *CommandHandler) handleExtend(userID id.UserID, gameName string) {
	if gameName == "" {
		h.reply(userID, "Usage: !extend <game name>")
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
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("  - %s (expires %s)\n", e.GameName, e.ExpiresAt.Format("January 2, 2006")))
	}
	sb.WriteString("\nCommands: !watch, !unwatch, !extend, !watchlist")

	h.reply(userID, sb.String())
}

func (h *CommandHandler) handleHelp(userID id.UserID) {
	h.reply(userID, "Watchlist commands:\n"+
		"  !watch <game name> — Watch for deals on a game\n"+
		"  !unwatch <game name> — Remove a game from your watchlist\n"+
		"  !extend <game name> — Reset the 180-day expiry timer\n"+
		"  !watchlist — Show your current watches\n"+
		"  !help — Show this message\n\n"+
		"Watches expire after 180 days. You'll get a reminder 7 days before.")
}

func (h *CommandHandler) reply(userID id.UserID, text string) {
	if err := h.sender.SendDM(userID, text); err != nil {
		slog.Error("watchlist: failed to send DM", "user", userID, "error", err)
	}
}
