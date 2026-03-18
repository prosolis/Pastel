# Pastel

A Matrix bot that posts gaming deals and free game alerts to a specified Matrix room, with a personal watchlist feature via DMs.

Deals are sourced from PC/digital storefronts only (Steam, GOG, Humble Store, GreenManGaming, Epic Games Store) — universally accessible regardless of region.

## Data Sources

- **CheapShark** — polled every 2 hours for top deals across Steam, GOG, Humble Store, and GreenManGaming
- **IsThereAnyDeal** — polled every 2 hours for deals across all tracked stores, with built-in historical low detection (requires API key)
- **Epic Games Store** — polled daily for free game promotions

CheapShark and IsThereAnyDeal can be used individually or together — configure via the `DEAL_SOURCES` variable. When used as a deal source, IsThereAnyDeal provides historical low flags directly. When only CheapShark is active, IsThereAnyDeal can still optionally enrich deals with historical low info via the `ITAD_API_KEY`.

## Quick Start

1. Copy `.env.example` to `.env` and fill in your Matrix credentials and room ID
2. Build and run with Go 1.25+:

```bash
go build -tags goolm -o pastel ./cmd/pastel
./pastel
```

Or run with Docker:

```bash
docker build -t pastel .
docker run --env-file .env -v pastel-data:/data pastel
```

## Watchlist

Users can DM the bot to set up personal deal alerts. When a matching deal appears, the bot sends a DM notification.

| Command | Description |
|---|---|
| `!search <game name>` | Search for current deals on a game |
| `!watch <game name>` | Watch for deals on a game |
| `!unwatch <# or game name>` | Remove a game from your watchlist |
| `!extend <# or game name>` | Reset the 180-day expiry timer |
| `!watchlist` | Show your numbered watchlist |
| `!help` | List available commands |

- Watches expire after **180 days** to prevent stale entries from accumulating
- The bot sends a reminder **7 days before expiry** — reply with `!extend` to keep it
- Matching uses normalized substring search, so watching "elden ring" will match "ELDEN RING: Shadow of the Erdtree"
- Notifications are sent via encrypted DMs (E2EE)
- `!unwatch` and `!extend` accept either the game name or a number from `!watchlist`
- `!search` is rate-limited to 5 searches per 10 minutes per user

## Obtaining a Matrix Bot Access Token

1. **Create a bot account** on your homeserver (e.g. via Element: register a new account like `@dealsbot:example.com`).

2. **Log in and get the access token** using `curl`:

   ```bash
   curl -XPOST "https://matrix.example.com/_matrix/client/v3/login" \
     -H "Content-Type: application/json" \
     -d '{
       "type": "m.login.password",
       "identifier": { "type": "m.id.user", "user": "dealsbot" },
       "password": "YOUR_PASSWORD"
     }'
   ```

   The response will contain an `access_token` field — copy that value into your `.env` as `MATRIX_BOT_ACCESS_TOKEN`.

3. **Invite the bot** to your deals room, then have the bot **accept the invite** (the bot does this automatically on startup).

> **Tip:** After extracting the token you can change the bot account's password without invalidating the token. Store the token securely — anyone with it can act as the bot.

## Configuration

All configuration is via environment variables (see `.env.example`):

### Core

| Variable | Required | Default | Description |
|---|---|---|---|
| `MATRIX_HOMESERVER_URL` | Yes | — | Matrix homeserver URL |
| `MATRIX_BOT_USER_ID` | Yes | — | Bot's Matrix user ID |
| `MATRIX_BOT_ACCESS_TOKEN` | Yes | — | Bot's access token |
| `MATRIX_BOT_PASSWORD` | No | — | Bot's password (enables auto-refresh, cross-signing, and device persistence) |
| `MATRIX_DEALS_ROOM_ID` | Yes | — | Room ID to post deals in |
| `ITAD_API_KEY` | No | — | IsThereAnyDeal API key (required when `itad` is in `DEAL_SOURCES`, optional otherwise for historical low detection) |
| `DEAL_SOURCES` | No | cheapshark | Comma-separated deal sources: `cheapshark`, `itad`, or `cheapshark,itad` |
| `MIN_DEAL_RATING` | No | 8.0 | Minimum CheapShark deal rating (0-10) |
| `MIN_DISCOUNT_PERCENT` | No | 50 | Minimum discount percentage |
| `MAX_PRICE_USD` | No | 20 | Maximum sale price in USD |
| `SEND_INTRO_MESSAGE` | No | false | Send "The deals must flow." to the room on startup |
| `DATABASE_PATH` | No | deals.db | Path to SQLite database file |

## Deployment

### systemd

A service file is included for systemd deployments:

```bash
# Build and install
go build -tags goolm -o pastel ./cmd/pastel
sudo mkdir -p /opt/pastel
sudo cp pastel /opt/pastel/
sudo cp .env /opt/pastel/

# Install and start the service
sudo cp pastel.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pastel
```

The service runs as a hardened unit with `ProtectSystem=strict`, restricting writes to `/opt/pastel` only. Adjust `WorkingDirectory` in the service file if deploying elsewhere.

```bash
# Check status
sudo systemctl status pastel

# View logs
sudo journalctl -u pastel -f
```

## Preflight Check

Run `--check` to validate your configuration and test connectivity to all services before starting the bot:

```bash
./pastel --check
```

This verifies:

- **Matrix** — authentication token is valid and bot has joined the target room
- **CheapShark** — API is reachable (skipped if not in `DEAL_SOURCES`)
- **Epic Games Store** — API is reachable
- **Frankfurter** — exchange rate API is reachable
- **IsThereAnyDeal** — API key is valid (required when `itad` is in `DEAL_SOURCES`, skipped otherwise if not configured)

The command exits with code 0 on success and 1 on failure, so it works in CI and Docker health-checks.

## Threads

Deals are posted inside per-category threads to keep the room organized:

| Thread | Content |
|---|---|
| Game Deals | CheapShark deals + ITAD deals with type `game` |
| DLC Deals | ITAD deals with type `dlc` |
| Epic Free Games | Current and upcoming free games from the Epic Games Store |

Thread root messages are created automatically the first time a deal in that category appears. The root event IDs are stored in the database so subsequent deals are posted into the same threads.

## Behavior

- **First run**: fetches current deals and records them in the database without posting (avoids spamming the room with existing deals)
- **Deduplication**: deals are tracked by game ID + timestamp; duplicates are never reposted
- **Pruning**: deals older than 30 days are pruned from the database so they can be reposted if they return
- **One message per deal**: each deal is posted individually so messages are independently linkable and dismissible
- **Multi-currency pricing**: deal prices are shown in USD, CAD, EUR, and GBP using live exchange rates from the [Frankfurter API](https://api.frankfurter.dev) (ECB data, no API key required). Rates are cached and refreshed twice daily.
- **E2EE support**: persistent crypto store via mautrix CryptoHelper for encrypted room and DM support
- **Auto-refresh**: when `MATRIX_BOT_PASSWORD` is set, the bot persists device credentials and automatically re-authenticates if the token expires
- **Cross-signing**: the bot bootstraps cross-signing on startup so its device is automatically verified
- **Presence heartbeat**: keeps the bot shown as online in Matrix clients

## Migrating from the Python Version

The Go version is compatible with the existing Python `deals.db`. A migration script is included to convert timestamp formats and create the new watchlist table:

```bash
# Build the migration tool
go build -o migrate ./cmd/migrate

# Migrate (creates a .bak backup automatically)
./migrate deals.db
```

The script:
- Creates a backup at `deals.db.bak`
- Converts Python's timestamp formats (`YYYY-MM-DD HH:MM:SS`, `YYYY-MM-DDTHH:MM:SS+00:00`) to RFC 3339
- Creates the `watchlist` table

All posted deals and the first-run flag carry over — no duplicate posts on switchover.
