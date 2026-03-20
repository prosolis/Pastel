package main

import (
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"maunium.net/go/mautrix/id"

	"github.com/prosolis/Pastel/internal/config"
	"github.com/prosolis/Pastel/internal/currency"
	"github.com/prosolis/Pastel/internal/database"
	"github.com/prosolis/Pastel/internal/deals"
	"github.com/prosolis/Pastel/internal/formatter"
	"github.com/prosolis/Pastel/internal/matrix"
	"github.com/prosolis/Pastel/internal/preflight"
	"github.com/prosolis/Pastel/internal/watchlist"
)

const (
	threadKeyGameDeals = "thread_game_deals"
	threadKeyDLCDeals  = "thread_dlc_deals"
	threadKeyEpicFree  = "thread_epic_free"
)

// getOrCreateThread retrieves an existing thread event ID from the DB,
// or creates a new thread root message and stores its ID.
func getOrCreateThread(db *database.DB, mx *matrix.Client, roomID, dbKey, title string) (string, error) {
	eventID, err := db.GetConfig(dbKey)
	if err == nil && eventID != "" {
		return eventID, nil
	}

	eventID, err = mx.CreateThread(roomID, title)
	if err != nil {
		return "", err
	}

	if err := db.SetConfig(dbKey, eventID); err != nil {
		slog.Warn("failed to persist thread event ID", "key", dbKey, "error", err)
	}

	slog.Info("created thread", "title", title, "event_id", eventID)
	return eventID, nil
}

func main() {
	checkFlag := flag.Bool("check", false, "Run preflight checks and exit")
	debugFlag := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debugFlag {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if *checkFlag {
		fmt.Println("Running preflight checks...")
		results := preflight.Run(cfg)
		if !preflight.PrintResults(results) {
			os.Exit(1)
		}
		fmt.Println("All checks passed.")
		return
	}

	// Open database
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Initialize watchlist store
	watchStore := watchlist.NewStore(db.RawDB())

	// Initialize Matrix client with E2EE and auto-refresh
	mx, err := matrix.New(matrix.ClientConfig{
		HomeserverURL: cfg.MatrixHomeserverURL,
		UserID:        cfg.MatrixBotUserID,
		AccessToken:   cfg.MatrixBotAccessToken,
		Password:      cfg.MatrixBotPassword,
		CryptoDBPath:  "crypto.db",
		DevicePath:    "device.json",
	})
	if err != nil {
		slog.Error("failed to create matrix client", "error", err)
		os.Exit(1)
	}
	defer mx.Stop()

	// Pre-fetch exchange rates
	conv := currency.NewConverter()
	conv.EnsureRates()

	// Set up watchlist command handler and register DM event handler
	cmdHandler := watchlist.NewCommandHandler(watchStore, mx, conv)
	dealsRoomID := id.RoomID(cfg.MatrixDealsRoomID)
	mx.RegisterMessageHandler(func(senderID id.UserID, roomID id.RoomID, body string) {
		// Only handle DMs, not messages in the deals room
		if roomID == dealsRoomID {
			return
		}
		cmdHandler.HandleMessage(string(senderID), body)
	})

	// Start sync loop after handlers are registered
	mx.StartSync()

	// First-run: populate DB without posting
	firstRunDone, _ := db.IsFirstRunDone()
	if !firstRunDone {
		slog.Info("first run detected — populating database without posting")
		populateInitialState(cfg, db)
		if err := db.SetFirstRunDone(); err != nil {
			slog.Error("failed to set first run done", "error", err)
		}
	}

	// Send intro message
	if cfg.SendIntroMessage {
		if err := mx.SendNotice(cfg.MatrixDealsRoomID, "The deals must flow."); err != nil {
			slog.Warn("failed to send intro message", "error", err)
		}
	}

	// Start presence heartbeat
	mx.StartPresenceHeartbeat()

	// Run initial checks
	slog.Info("running initial deal checks")
	if cfg.HasSource("cheapshark") {
		checkCheapShark(cfg, db, mx, conv, watchStore)
	}
	if cfg.HasSource("itad") {
		checkITADDeals(cfg, db, mx, conv, watchStore)
	}
	checkEpicFreeGames(cfg, db, mx, watchStore)

	// Run initial expiry check
	checkWatchlistExpiry(watchStore, mx)

	// Start ticker goroutines
	stop := make(chan struct{})

	if cfg.HasSource("cheapshark") {
		go func() {
			ticker := time.NewTicker(2 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					checkCheapShark(cfg, db, mx, conv, watchStore)
				}
			}
		}()
	}

	if cfg.HasSource("itad") {
		go func() {
			ticker := time.NewTicker(2 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					checkITADDeals(cfg, db, mx, conv, watchStore)
				}
			}
		}()
	}

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				checkEpicFreeGames(cfg, db, mx, watchStore)
			}
		}
	}()

	// Watchlist expiry check — daily
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				checkWatchlistExpiry(watchStore, mx)
			}
		}
	}()

	slog.Info("bot is running", "sources", cfg.DealSources)

	// Wait for OS signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutting down")
	close(stop)
}

func populateInitialState(cfg *config.Config, db *database.DB) {
	// Record CheapShark deals without posting
	if cfg.HasSource("cheapshark") {
		rawDeals, err := deals.FetchCheapSharkDeals(cfg.MaxPriceUSD, 60)
		if err != nil {
			slog.Warn("first run: cheapshark fetch failed", "error", err)
		} else {
			filtered := deals.FilterCheapSharkDeals(rawDeals, cfg.MinDealRating, cfg.MinDiscountPercent, cfg.MaxPriceUSD)
			for _, d := range filtered {
				_ = db.MarkPosted(d.DedupID, "cheapshark", d.Title)
			}
			slog.Info("first run: populated cheapshark deals", "count", len(filtered))
		}
	}

	// Record ITAD deals without posting
	if cfg.HasSource("itad") && cfg.ITADAPIKey != "" {
		itadDeals, err := deals.FetchITADDeals(cfg.ITADAPIKey, 20)
		if err != nil {
			slog.Warn("first run: itad fetch failed", "error", err)
		} else {
			filtered := deals.FilterITADDeals(itadDeals, cfg.MinDiscountPercent, cfg.MaxPriceUSD)
			for _, d := range filtered {
				_ = db.MarkPosted(d.DedupID, "itad", d.Title)
			}
			slog.Info("first run: populated itad deals", "count", len(filtered))
		}
	}

	// Epic games are intentionally NOT recorded on first run
	// so they post immediately (few games, time-limited)
}

func notifyWatchlistMatches(ws *watchlist.Store, mx *matrix.Client, title, url, price string, discount int) {
	matches, err := ws.FindMatchingUsers(title)
	if err != nil {
		slog.Error("watchlist match failed", "error", err)
		return
	}
	for _, m := range matches {
		msg := formatter.FormatWatchlistNotification(m.GameName, title, url, price, discount)
		if err := mx.SendDM(id.UserID(m.UserID), msg); err != nil {
			slog.Error("failed to send watchlist DM", "user", m.UserID, "error", err)
		}
	}
}

func notifyWatchlistFreeMatches(ws *watchlist.Store, mx *matrix.Client, title, url string) {
	matches, err := ws.FindMatchingUsers(title)
	if err != nil {
		slog.Error("watchlist match failed", "error", err)
		return
	}
	for _, m := range matches {
		msg := formatter.FormatWatchlistFreeNotification(m.GameName, title, url)
		if err := mx.SendDM(id.UserID(m.UserID), msg); err != nil {
			slog.Error("failed to send watchlist DM", "user", m.UserID, "error", err)
		}
	}
}

func checkCheapShark(cfg *config.Config, db *database.DB, mx *matrix.Client, conv *currency.Converter, ws *watchlist.Store) {
	slog.Debug("checking cheapshark deals")
	conv.EnsureRates()

	rawDeals, err := deals.FetchCheapSharkDeals(cfg.MaxPriceUSD, 60)
	if err != nil {
		slog.Error("cheapshark fetch failed", "error", err)
		return
	}

	filtered := deals.FilterCheapSharkDeals(rawDeals, cfg.MinDealRating, cfg.MinDiscountPercent, cfg.MaxPriceUSD)

	// Look up historical lows if ITAD key is available
	if cfg.ITADAPIKey != "" {
		var steamIDs []string
		steamIDSet := make(map[string]bool)
		for _, d := range filtered {
			if d.SteamAppID != "" && d.SteamAppID != "0" && !steamIDSet[d.SteamAppID] {
				steamIDs = append(steamIDs, d.SteamAppID)
				steamIDSet[d.SteamAppID] = true
			}
		}
		if len(steamIDs) > 0 {
			lows, err := deals.LookupHistoricalLows(cfg.ITADAPIKey, steamIDs)
			if err != nil {
				slog.Warn("historical low lookup failed", "error", err)
			} else {
				for i := range filtered {
					if isLow, ok := lows[filtered[i].SteamAppID]; ok && isLow {
						filtered[i].IsHistLow = true
					}
				}
			}
		}
	}

	threadID, err := getOrCreateThread(db, mx, cfg.MatrixDealsRoomID, threadKeyGameDeals, "Game Deals")
	if err != nil {
		slog.Error("failed to get/create game deals thread", "error", err)
		return
	}

	posted := 0
	for _, d := range filtered {
		already, err := db.IsPosted(d.DedupID)
		if err != nil {
			slog.Error("db check failed", "error", err)
			continue
		}
		if already {
			continue
		}

		msg := formatter.FormatCheapSharkDeal(d, conv)
		if err := mx.SendDealInThread(cfg.MatrixDealsRoomID, threadID, msg.Plain, msg.HTML); err != nil {
			slog.Error("failed to send cheapshark deal", "title", d.Title, "error", err)
			continue
		}

		if err := db.MarkPosted(d.DedupID, "cheapshark", d.Title); err != nil {
			slog.Error("failed to mark deal posted", "error", err)
		}
		posted++

		// Notify watchlist matches
		notifyWatchlistMatches(ws, mx, d.Title, d.DealURL, conv.FormatPrice(d.SalePrice), int(math.Floor(d.Savings)))
	}

	if posted > 0 {
		slog.Info("posted cheapshark deals", "count", posted)
	}

	if err := db.PruneOldDeals(30); err != nil {
		slog.Warn("failed to prune old deals", "error", err)
	}
}

func checkITADDeals(cfg *config.Config, db *database.DB, mx *matrix.Client, conv *currency.Converter, ws *watchlist.Store) {
	if cfg.ITADAPIKey == "" {
		return
	}

	slog.Debug("checking itad deals")
	conv.EnsureRates()

	itadDeals, err := deals.FetchITADDeals(cfg.ITADAPIKey, 20)
	if err != nil {
		slog.Error("itad fetch failed", "error", err)
		return
	}

	filtered := deals.FilterITADDeals(itadDeals, cfg.MinDiscountPercent, cfg.MaxPriceUSD)

	gameThreadID, err := getOrCreateThread(db, mx, cfg.MatrixDealsRoomID, threadKeyGameDeals, "Game Deals")
	if err != nil {
		slog.Error("failed to get/create game deals thread", "error", err)
		return
	}
	dlcThreadID, err := getOrCreateThread(db, mx, cfg.MatrixDealsRoomID, threadKeyDLCDeals, "DLC Deals")
	if err != nil {
		slog.Error("failed to get/create dlc deals thread", "error", err)
		return
	}

	posted := 0
	for _, d := range filtered {
		already, err := db.IsPosted(d.DedupID)
		if err != nil {
			slog.Error("db check failed", "error", err)
			continue
		}
		if already {
			continue
		}

		threadID := gameThreadID
		if strings.EqualFold(d.Type, "dlc") {
			threadID = dlcThreadID
		}

		msg := formatter.FormatITADDeal(d, conv)
		if err := mx.SendDealInThread(cfg.MatrixDealsRoomID, threadID, msg.Plain, msg.HTML); err != nil {
			slog.Error("failed to send itad deal", "title", d.Title, "error", err)
			continue
		}

		if err := db.MarkPosted(d.DedupID, "itad", d.Title); err != nil {
			slog.Error("failed to mark deal posted", "error", err)
		}
		posted++

		// Notify watchlist matches
		notifyWatchlistMatches(ws, mx, d.Title, d.URL, conv.FormatPrice(d.Price), d.Discount)
	}

	if posted > 0 {
		slog.Info("posted itad deals", "count", posted)
	}

	if err := db.PruneOldDeals(30); err != nil {
		slog.Warn("failed to prune old deals", "error", err)
	}
}

func checkEpicFreeGames(cfg *config.Config, db *database.DB, mx *matrix.Client, ws *watchlist.Store) {
	slog.Debug("checking epic free games")

	games, err := deals.FetchEpicFreeGames()
	if err != nil {
		slog.Error("epic fetch failed", "error", err)
		return
	}

	threadID, err := getOrCreateThread(db, mx, cfg.MatrixDealsRoomID, threadKeyEpicFree, "Epic Free Games")
	if err != nil {
		slog.Error("failed to get/create epic free games thread", "error", err)
		return
	}

	posted := 0
	for _, g := range games {
		already, err := db.IsPosted(g.DedupID)
		if err != nil {
			slog.Error("db check failed", "error", err)
			continue
		}
		if already {
			continue
		}

		msg := formatter.FormatEpicFreeGame(g)
		if err := mx.SendDealInThread(cfg.MatrixDealsRoomID, threadID, msg.Plain, msg.HTML); err != nil {
			slog.Error("failed to send epic game", "title", g.Title, "error", err)
			continue
		}

		if err := db.MarkPosted(g.DedupID, "epic", g.Title); err != nil {
			slog.Error("failed to mark epic game posted", "error", err)
		}
		posted++

		// Notify watchlist matches
		notifyWatchlistFreeMatches(ws, mx, g.Title, g.URL)
	}

	if posted > 0 {
		slog.Info("posted epic games", "count", posted)
	}
}

func checkWatchlistExpiry(ws *watchlist.Store, mx *matrix.Client) {
	// Warn users about entries expiring within 7 days
	expiring, err := ws.GetExpiringWatches(7)
	if err != nil {
		slog.Error("failed to check expiring watches", "error", err)
		return
	}

	for _, e := range expiring {
		msg := fmt.Sprintf("Your watch for \"%s\" expires on %s. Send !extend %s to keep it for another 180 days.",
			e.GameName, e.ExpiresAt.Format("January 2, 2006"), e.GameName)
		if err := mx.SendDM(id.UserID(e.UserID), msg); err != nil {
			slog.Error("failed to send expiry warning", "user", e.UserID, "error", err)
			continue
		}
		if err := ws.MarkExpiryWarned(e.ID); err != nil {
			slog.Error("failed to mark expiry warned", "error", err)
		}
	}

	// Purge fully expired entries
	purged, err := ws.PurgeExpired()
	if err != nil {
		slog.Error("failed to purge expired watches", "error", err)
	} else if purged > 0 {
		slog.Info("purged expired watchlist entries", "count", purged)
	}
}
