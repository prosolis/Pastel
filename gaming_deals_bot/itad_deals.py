"""IsThereAnyDeal deals list — fetch current deals from the ITAD API."""

import logging
from dataclasses import dataclass

import httpx

from .itad import BASE_URL

logger = logging.getLogger(__name__)

# ITAD shop ID for Steam
STEAM_SHOP_ID = 61


@dataclass
class ITADDeal:
    game_id: str  # ITAD UUID
    slug: str
    title: str
    sale_price: float  # current deal price
    normal_price: float  # regular (non-sale) price
    discount: int  # percentage off (0-100)
    currency: str
    shop_name: str
    shop_id: int
    url: str  # purchase redirect URL
    is_historical_low: bool
    timestamp: str  # ISO datetime of deal
    expiry: str | None  # ISO datetime when deal expires

    @property
    def dedup_id(self) -> str:
        return f"itad-{self.game_id}-{self.shop_id}-{self.discount}"

    @property
    def sale_price_usd(self) -> str:
        return f"{self.sale_price:.2f}"

    @property
    def normal_price_usd(self) -> str:
        return f"{self.normal_price:.2f}"


async def fetch_deals(
    client: httpx.AsyncClient,
    api_key: str,
    *,
    max_price: float = 20,
    min_discount: float = 50,
    limit: int = 20,
) -> list[ITADDeal]:
    """Fetch current deals from IsThereAnyDeal.

    Uses GET /deals/v2 with sorting by highest discount.
    """
    if not api_key:
        return []

    params: dict = {
        "key": api_key,
        "sort": "-cut",
        "limit": min(limit, 200),
        "nondeals": "false",
    }

    try:
        resp = await client.get(f"{BASE_URL}/deals/v2", params=params)
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("ITAD deals API error: %s", exc)
        return []

    raw_list = data.get("list", [])
    logger.info("ITAD returned %d raw deals (before filtering)", len(raw_list))

    deals: list[ITADDeal] = []
    for entry in raw_list:
        deal_data = entry.get("deal", {})
        if not deal_data:
            continue

        cut = deal_data.get("cut", 0)
        price_amount = deal_data.get("price", {}).get("amount", 0)
        regular_amount = deal_data.get("regular", {}).get("amount", 0)
        currency = deal_data.get("price", {}).get("currency", "USD")
        title = entry.get("title", "?")

        # Apply filters
        if cut < min_discount:
            logger.debug(
                "Filtered out %s: discount %d%% < %d%%", title, cut, int(min_discount)
            )
            continue
        if price_amount > max_price:
            logger.debug(
                "Filtered out %s: price %.2f > %.2f", title, price_amount, max_price
            )
            continue

        flag = deal_data.get("flag")
        is_historical_low = flag in ("H", "N")

        shop = deal_data.get("shop", {})

        deals.append(
            ITADDeal(
                game_id=entry.get("id", ""),
                slug=entry.get("slug", ""),
                title=title,
                sale_price=price_amount,
                normal_price=regular_amount,
                discount=cut,
                currency=currency,
                shop_name=shop.get("name", "Unknown"),
                shop_id=shop.get("id", 0),
                url=deal_data.get("url", ""),
                is_historical_low=is_historical_low,
                timestamp=deal_data.get("timestamp", ""),
                expiry=deal_data.get("expiry"),
            )
        )

    logger.info("ITAD returned %d deals after filtering", len(deals))
    return deals
