import logging
from dataclasses import dataclass
from datetime import datetime, timezone

import httpx

logger = logging.getLogger(__name__)

FREE_GAMES_URL = (
    "https://store-site-backend-static.ak.epicgames.com/freeGamesPromotions"
)


@dataclass
class EpicFreeGame:
    title: str
    game_id: str
    description: str
    end_date: str | None  # ISO datetime when the offer expires
    url: str

    @property
    def dedup_id(self) -> str:
        return f"epic-{self.game_id}"


def _parse_promotions(
    elements: list[dict],
) -> tuple[list[EpicFreeGame], list[EpicFreeGame]]:
    """Parse Epic promotions into current free games and upcoming free games."""
    current: list[EpicFreeGame] = []
    upcoming: list[EpicFreeGame] = []
    now = datetime.now(timezone.utc)

    for elem in elements:
        title = elem.get("title", "Unknown")
        game_id = elem.get("id", "")
        description = elem.get("description", "")

        # Build the store URL from the slug or product slug
        slug = (
            elem.get("productSlug")
            or elem.get("urlSlug")
            or elem.get("catalogNs", {}).get("mappings", [{}])[0].get("pageSlug", "")
        )
        url = f"https://store.epicgames.com/en-US/p/{slug}" if slug else ""

        # Check current promotional offers
        promotions = elem.get("promotions")
        if not promotions:
            continue

        # Current offers
        for offer_group in promotions.get("promotionalOffers", []):
            for offer in offer_group.get("promotionalOffers", []):
                discount = offer.get("discountSetting", {})
                if discount.get("discountPercentage", 100) == 0:
                    end_date = offer.get("endDate")
                    # Verify the offer is currently active
                    start = offer.get("startDate")
                    if start:
                        start_dt = datetime.fromisoformat(
                            start.replace("Z", "+00:00")
                        )
                        if start_dt > now:
                            continue
                    if end_date:
                        end_dt = datetime.fromisoformat(
                            end_date.replace("Z", "+00:00")
                        )
                        if end_dt < now:
                            continue

                    current.append(
                        EpicFreeGame(
                            title=title,
                            game_id=game_id,
                            description=description,
                            end_date=end_date,
                            url=url,
                        )
                    )

        # Upcoming offers
        for offer_group in promotions.get("upcomingPromotionalOffers", []):
            for offer in offer_group.get("promotionalOffers", []):
                discount = offer.get("discountSetting", {})
                if discount.get("discountPercentage", 100) == 0:
                    end_date = offer.get("endDate")
                    upcoming.append(
                        EpicFreeGame(
                            title=title,
                            game_id=game_id,
                            description=description,
                            end_date=end_date,
                            url=url,
                        )
                    )

    return current, upcoming


async def fetch_free_games(
    client: httpx.AsyncClient,
) -> tuple[list[EpicFreeGame], list[EpicFreeGame]]:
    """Fetch current and upcoming free games from Epic Games Store.

    Returns (current_free_games, upcoming_free_games).
    """
    try:
        resp = await client.get(FREE_GAMES_URL, params={"locale": "en-US"})
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("Epic Games Store API error: %s", exc)
        return [], []

    elements = (
        data.get("data", {})
        .get("Catalog", {})
        .get("searchStore", {})
        .get("elements", [])
    )

    if not elements:
        logger.warning("No elements found in Epic free games response")
        return [], []

    current, upcoming = _parse_promotions(elements)
    logger.info(
        "Epic: %d current free games, %d upcoming",
        len(current),
        len(upcoming),
    )
    return current, upcoming
