# Pastel

A Matrix bot that posts gaming deals and free game alerts to a specified Matrix room.

Deals are sourced from PC/digital storefronts only (Steam, GOG, Humble Store, GreenManGaming, Epic Games Store) — universally accessible regardless of region.

## Data Sources

- **CheapShark** — polled every 2 hours for top deals across Steam, GOG, Humble Store, and GreenManGaming
- **Epic Games Store** — polled daily for free game promotions
- **IsThereAnyDeal** (optional) — flags deals that are at an all-time historical low price

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

## Configuration

All configuration is via environment variables (see `.env.example`):

| Variable | Required | Default | Description |
|---|---|---|---|
| `MATRIX_HOMESERVER_URL` | Yes | — | Matrix homeserver URL |
| `MATRIX_BOT_USER_ID` | Yes | — | Bot's Matrix user ID |
| `MATRIX_BOT_ACCESS_TOKEN` | Yes | — | Bot's access token |
| `MATRIX_DEALS_ROOM_ID` | Yes | — | Room ID to post deals in |
| `ITAD_API_KEY` | No | — | IsThereAnyDeal API key for historical low detection |
| `MIN_DEAL_RATING` | No | 80 | Minimum CheapShark deal rating (0-100) |
| `MIN_DISCOUNT_PERCENT` | No | 50 | Minimum discount percentage |
| `MAX_PRICE_USD` | No | 20 | Maximum sale price in USD |
| `DATABASE_PATH` | No | deals.db | Path to SQLite database file |

## Behavior

- **First run**: fetches current deals and records them in the database without posting (avoids spamming the room with existing deals)
- **Deduplication**: deals are tracked by game ID + timestamp; duplicates are never reposted
- **Pruning**: deals older than 30 days are pruned from the database so they can be reposted if they return
- **One message per deal**: each deal is posted individually so messages are independently linkable and dismissible
- **Multi-currency pricing**: deal prices are shown in USD, CAD, EUR, and GBP using live exchange rates from the [Frankfurter API](https://api.frankfurter.dev) (ECB data, no API key required). Rates are cached and refreshed hourly.
