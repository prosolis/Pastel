"""IsThereAnyDeal deals list — fetch current deals from the ITAD API."""

import logging
from dataclasses import dataclass

import httpx

from .currency import convert_to_usd
from .itad import BASE_URL

logger = logging.getLogger(__name__)

# ITAD shop ID for Steam
STEAM_SHOP_ID = 61


@dataclass
class ITADDeal:
    game_id: str  # ITAD UUID
    slug: str
    title: str
    sale_price: float  # current deal price (normalised to USD)
    normal_price: float  # regular (non-sale) price (normalised to USD)
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
    countries: list[str] | None = None,
    max_price: float = 20,
    min_discount: float = 50,
    limit: int = 100,
) -> list[ITADDeal]:
    """Fetch current deals from IsThereAnyDeal across one or more countries.

    Deals are fetched per-country, merged (first country in the list wins
    when the same game+shop appears in multiple regions), and finally sorted
    by timestamp so that the newest deals appear first.
    """
    if not api_key:
        return []

    if countries is None:
        countries = ["US"]

    seen: set[str] = set()  # game_id-shop_id keys for cross-country dedup
    all_deals: list[ITADDeal] = []

    for country in countries:
        country_deals = await _fetch_country_deals(
            client,
            api_key,
            country=country,
            max_price=max_price,
            min_discount=min_discount,
            limit=limit,
        )
        for deal in country_deals:
            key = f"{deal.game_id}-{deal.shop_id}"
            if key not in seen:
                seen.add(key)
                all_deals.append(deal)
            else:
                logger.debug(
                    "Skipping duplicate %s from country %s", deal.title, country
                )

    # Sort newest first — the API only supports sorting by discount or price,
    # so we sort client-side by the deal timestamp.
    all_deals.sort(key=lambda d: d.timestamp, reverse=True)

    logger.info(
        "ITAD: %d deals across %d country/countries after dedup",
        len(all_deals),
        len(countries),
    )
    return all_deals


async def _fetch_country_deals(
    client: httpx.AsyncClient,
    api_key: str,
    *,
    country: str = "US",
    max_price: float = 20,
    min_discount: float = 50,
    limit: int = 100,
) -> list[ITADDeal]:
    """Fetch deals for a single country from the ITAD ``/deals/v2`` endpoint."""
    params: dict = {
        "key": api_key,
        "country": country,
        "sort": "-cut",
        "limit": min(limit, 200),
        "nondeals": "false",
    }

    try:
        resp = await client.get(f"{BASE_URL}/deals/v2", params=params)
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("ITAD deals API error for country %s: %s", country, exc)
        return []

    raw_list = data.get("list", [])
    logger.info(
        "ITAD returned %d raw deals for %s (before filtering)", len(raw_list), country
    )

    deals: list[ITADDeal] = []
    for entry in raw_list:
        deal_data = entry.get("deal", {})
        if not deal_data:
            continue

        # Only include actual games and DLC — skip non-game content
        # (courses, software bundles, etc.)
        entry_type = entry.get("type")
        title = entry.get("title", "?")
        if entry_type not in ("game", "dlc"):
            logger.debug("Filtered out %s: type=%s", title, entry_type)
            continue

        cut = deal_data.get("cut", 0)
        price_amount = deal_data.get("price", {}).get("amount", 0)
        regular_amount = deal_data.get("regular", {}).get("amount", 0)
        currency = deal_data.get("price", {}).get("currency", "USD")

        # Normalise to USD so prices are comparable across regions
        price_usd = convert_to_usd(price_amount, currency)
        regular_usd = convert_to_usd(regular_amount, currency)

        # Apply ITAD-specific filters
        if cut < min_discount:
            logger.debug(
                "Filtered out %s: discount %d%% < %d%%",
                title,
                cut,
                int(min_discount),
            )
            continue
        if price_usd > max_price:
            logger.debug(
                "Filtered out %s: price $%.2f > $%.2f", title, price_usd, max_price
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
                sale_price=price_usd,
                normal_price=regular_usd,
                discount=cut,
                currency="USD",  # normalised
                shop_name=shop.get("name", "Unknown"),
                shop_id=shop.get("id", 0),
                url=deal_data.get("url", ""),
                is_historical_low=is_historical_low,
                timestamp=deal_data.get("timestamp", ""),
                expiry=deal_data.get("expiry"),
            )
        )

    logger.info(
        "ITAD returned %d deals for %s after filtering", len(deals), country
    )
    return deals
