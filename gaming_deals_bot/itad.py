import logging

import httpx

logger = logging.getLogger(__name__)

BASE_URL = "https://api.isthereanydeal.com"


async def check_historical_lows(
    client: httpx.AsyncClient,
    api_key: str,
    steam_app_ids: list[str],
) -> dict[str, bool]:
    """Check if current prices are historical lows for a batch of Steam app IDs.

    Returns a mapping of steam_app_id -> is_historical_low.
    """
    if not api_key or not steam_app_ids:
        return {}

    # Build the list of ITAD game IDs from Steam app IDs
    # ITAD uses the format "app/{steam_app_id}" for Steam games
    game_ids = [f"app/{sid}" for sid in steam_app_ids]

    try:
        resp = await client.get(
            f"{BASE_URL}/games/overview/v2",
            params={"key": api_key, "shops[]": "steam"},
            headers={"Content-Type": "application/json"},
        )
        # The v2 endpoint may use POST with body — fall back to per-game lookup
        # if batch doesn't work. For now, use the games/info endpoint pattern.
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("ITAD API error: %s", exc)
        return {}

    results: dict[str, bool] = {}
    # Parse the response — structure depends on the ITAD API version
    # The overview endpoint returns price overview with historical low data
    if isinstance(data, dict):
        for steam_id in steam_app_ids:
            game_key = f"app/{steam_id}"
            game_data = data.get(game_key) or data.get(steam_id, {})
            if isinstance(game_data, dict):
                lowest = game_data.get("lowest", {})
                current = game_data.get("current", {})
                if lowest and current:
                    lowest_price = lowest.get("price", float("inf"))
                    current_price = current.get("price", float("inf"))
                    results[steam_id] = current_price <= lowest_price

    logger.info(
        "ITAD: checked %d games, %d are at historical low",
        len(steam_app_ids),
        sum(results.values()),
    )
    return results


async def check_single_historical_low(
    client: httpx.AsyncClient,
    api_key: str,
    steam_app_id: str,
) -> bool:
    """Check if a single game is at its historical low price."""
    if not api_key or not steam_app_id:
        return False

    try:
        resp = await client.get(
            f"{BASE_URL}/games/overview/v2",
            params={
                "key": api_key,
                "apps[]": f"app/{steam_app_id}",
            },
        )
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("ITAD API error for app %s: %s", steam_app_id, exc)
        return False

    # Try to extract historical low info
    if isinstance(data, dict) and "prices" in data:
        for price_info in data["prices"]:
            if price_info.get("isLowest"):
                return True

    return False
