# Pastel

A Matrix bot that posts gaming deals and free game alerts to a specified Matrix room.

Deals are sourced from PC/digital storefronts only (Steam, GOG, Humble Store, GreenManGaming, Epic Games Store) — universally accessible regardless of region.

## Data Sources

- **CheapShark** — polled every 2 hours for top deals across Steam, GOG, Humble Store, and GreenManGaming
- **IsThereAnyDeal** — polled every 2 hours for deals across all tracked stores, with built-in historical low detection (requires API key)
- **Epic Games Store** — polled daily for free game promotions

CheapShark and IsThereAnyDeal can be used individually or together — configure via the `DEAL_SOURCES` variable. When used as a deal source, IsThereAnyDeal provides historical low flags directly. When only CheapShark is active, IsThereAnyDeal can still optionally enrich deals with historical low info via the `ITAD_API_KEY`.

## Quick Start

1. Copy `.env.example` to `.env` and fill in your Matrix credentials and room ID
2. Run with Docker:

```bash
docker build -t pastel .
docker run --env-file .env -v pastel-data:/data pastel
```

Or run directly with Python 3.12+:

```bash
pip install -r requirements.txt
python -m gaming_deals_bot
```

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
| `MATRIX_DEALS_ROOM_ID` | Yes | — | Room ID to post deals in |
| `ITAD_API_KEY` | No | — | IsThereAnyDeal API key (required when `itad` is in `DEAL_SOURCES`, optional otherwise for historical low detection) |
| `DEAL_SOURCES` | No | cheapshark | Comma-separated deal sources: `cheapshark`, `itad`, or `cheapshark,itad` |
| `ITAD_COUNTRIES` | No | US | Comma-separated ISO 3166-1 alpha-2 country codes to fetch ITAD deals from (e.g. `US,CA,GB,DE`) |
| `DEFAULT_CURRENCY` | No | USD | Primary display currency shown first in price strings |
| `EXTRA_CURRENCIES` | No | CAD,EUR,GBP | Additional currencies shown after the default (comma-separated) |
| `SEND_INTRO_MESSAGE` | No | false | Send "The deals must flow." to the room on startup |
| `DATABASE_PATH` | No | deals.db | Path to SQLite database file |

### Filtering

Each deal source has its own filter settings. Source-specific values take priority; when not set they fall back to the shared defaults.

| Variable | Source | Default | Description |
|---|---|---|---|
| `CHEAPSHARK_MIN_DISCOUNT` | CheapShark | 50 | Minimum discount percentage |
| `CHEAPSHARK_MIN_RATING` | CheapShark | 8.0 | Minimum deal rating (0-10, 0 = unrated allowed) |
| `CHEAPSHARK_MAX_PRICE` | CheapShark | 20 | Maximum sale price (USD) |
| `ITAD_MIN_DISCOUNT` | ITAD | 50 | Minimum discount percentage |
| `ITAD_MAX_PRICE` | ITAD | 20 | Maximum sale price (USD, prices from other regions are converted) |
| `ITAD_DEALS_LIMIT` | ITAD | 200 | Number of deals to fetch per country (max 200) |
| `MIN_DISCOUNT_PERCENT` | Shared | 50 | Fallback minimum discount when source-specific value is not set |
| `MAX_PRICE` | Shared | 20 | Fallback maximum price when source-specific value is not set |

## Preflight Check

Run `--check` to validate your configuration and test connectivity to all services before starting the bot:

```bash
python -m gaming_deals_bot --check
```

This verifies:

- **Matrix** — authentication token is valid and bot has joined the target room
- **CheapShark** — API is reachable (skipped if not in `DEAL_SOURCES`)
- **Epic Games Store** — API is reachable
- **Frankfurter** — exchange rate API is reachable
- **IsThereAnyDeal** — API key is valid (required when `itad` is in `DEAL_SOURCES`, skipped otherwise if not configured)

The command exits with code 0 on success and 1 on failure, so it works in CI and Docker health-checks.

## Behavior

- **First run**: fetches current deals and records them in the database without posting (avoids spamming the room with existing deals)
- **Deduplication**: deals are tracked by game ID + timestamp; duplicates are never reposted
- **Pruning**: deals older than 30 days are pruned from the database so they can be reposted if they return
- **One message per deal**: each deal is posted individually so messages are independently linkable and dismissible
- **Multi-currency pricing**: deal prices are shown in your configured currencies (default: USD, CAD, EUR, GBP) using live exchange rates from the [Frankfurter API](https://api.frankfurter.dev) (ECB data, no API key required). Set `DEFAULT_CURRENCY` to change the primary display currency and `EXTRA_CURRENCIES` for additional ones. Rates are cached and refreshed twice daily.
- **Multi-country ITAD deals**: when using IsThereAnyDeal, deals can be fetched from multiple countries simultaneously via `ITAD_COUNTRIES` (e.g. `US,CA,GB,DE`). Deals are merged and deduplicated, with the first country in the list taking priority for duplicate games.
