import logging
from dataclasses import dataclass

import httpx

logger = logging.getLogger(__name__)

BASE_URL = "https://www.cheapshark.com/api/1.0"

# CheapShark store IDs for PC/digital storefronts
STORE_IDS = {
    "1": "Steam",
    "7": "GOG",
    "11": "Humble Store",
    "23": "GreenManGaming",
}


@dataclass
class CheapSharkDeal:
    deal_id: str
    game_id: str
    title: str
    sale_price: str
    normal_price: str
    savings: float  # percentage off
    deal_rating: float
    store_id: str
    last_change: int  # unix timestamp
    steam_app_id: str | None

    @property
    def store_name(self) -> str:
        return STORE_IDS.get(self.store_id, f"Store {self.store_id}")

    @property
    def deal_url(self) -> str:
        return f"https://www.cheapshark.com/redirect?dealID={self.deal_id}"

    @property
    def dedup_id(self) -> str:
        return f"cheapshark-{self.game_id}-{self.last_change}"


async def fetch_deals(
    client: httpx.AsyncClient,
    *,
    max_price: float = 20,
    min_rating: float = 8.0,
    min_discount: float = 50,
    page_size: int = 10,
) -> list[CheapSharkDeal]:
    """Fetch top deals from CheapShark across configured stores."""
    store_ids = ",".join(STORE_IDS.keys())
    params = {
        "storeID": store_ids,
        "upperPrice": str(int(max_price)),
        "sortBy": "Deal Rating",
        "desc": "1",
        "pageSize": str(page_size),
    }

    try:
        resp = await client.get(f"{BASE_URL}/deals", params=params)
        resp.raise_for_status()
        raw_deals = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("CheapShark API error: %s", exc)
        return []

    logger.info(
        "CheapShark returned %d raw deals (before filtering)", len(raw_deals),
    )

    deals = []
    for d in raw_deals:
        savings = float(d.get("savings", 0))
        rating = float(d.get("dealRating", 0))
        title = d.get("title", "?")

        if savings < min_discount:
            logger.debug("Filtered out %s: savings %.1f%% < %.1f%%", title, savings, min_discount)
            continue
        if rating > 0 and rating < min_rating:
            logger.debug("Filtered out %s: rating %.1f < %.1f", title, rating, min_rating)
            continue

        steam_app_id = d.get("steamAppID") or None
        if steam_app_id == "0":
            steam_app_id = None

        deals.append(
            CheapSharkDeal(
                deal_id=d["dealID"],
                game_id=d["gameID"],
                title=d["title"],
                sale_price=d["salePrice"],
                normal_price=d["normalPrice"],
                savings=savings,
                deal_rating=rating,
                store_id=d["storeID"],
                last_change=int(d.get("lastChange", 0)),
                steam_app_id=steam_app_id,
            )
        )

    logger.info("CheapShark returned %d deals after filtering", len(deals))
    return deals
