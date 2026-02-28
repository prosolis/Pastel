import logging

import httpx

logger = logging.getLogger(__name__)

BASE_URL = "https://api.isthereanydeal.com"


async def _lookup_game_id(
    client: httpx.AsyncClient,
    api_key: str,
    steam_app_id: str,
) -> str | None:
    """Look up the ITAD game UUID for a Steam app ID.

    Returns the ITAD game UUID string, or None if not found.
    """
    try:
        resp = await client.get(
            f"{BASE_URL}/games/lookup/v1",
            params={"key": api_key, "appid": steam_app_id},
        )
        resp.raise_for_status()
        data = resp.json()
        if data.get("found") and data.get("game"):
            return data["game"]["id"]
    except (httpx.HTTPError, ValueError, KeyError) as exc:
        logger.error("ITAD lookup error for app %s: %s", steam_app_id, exc)
    return None


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

    # Look up ITAD game UUIDs for each Steam app ID
    itad_id_to_steam: dict[str, str] = {}
    for sid in steam_app_ids:
        itad_id = await _lookup_game_id(client, api_key, sid)
        if itad_id:
            itad_id_to_steam[itad_id] = sid

    if not itad_id_to_steam:
        return {}

    try:
        resp = await client.post(
            f"{BASE_URL}/games/overview/v2",
            params={"key": api_key, "shops": [61]},
            json=list(itad_id_to_steam.keys()),
        )
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("ITAD API error: %s", exc)
        return {}

    results: dict[str, bool] = {}
    for price_entry in data.get("prices", []):
        itad_id = price_entry.get("id")
        steam_id = itad_id_to_steam.get(itad_id)
        if not steam_id:
            continue
        lowest = price_entry.get("lowest")
        current = price_entry.get("current")
        if lowest and current:
            lowest_price = lowest.get("price", {}).get("amount", float("inf"))
            current_price = current.get("price", {}).get("amount", float("inf"))
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

    itad_id = await _lookup_game_id(client, api_key, steam_app_id)
    if not itad_id:
        return False

    try:
        resp = await client.post(
            f"{BASE_URL}/games/overview/v2",
            params={"key": api_key},
            json=[itad_id],
        )
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.error("ITAD API error for app %s: %s", steam_app_id, exc)
        return False

    for price_entry in data.get("prices", []):
        lowest = price_entry.get("lowest")
        current = price_entry.get("current")
        if lowest and current:
            lowest_price = lowest.get("price", {}).get("amount", float("inf"))
            current_price = current.get("price", {}).get("amount", float("inf"))
            if current_price <= lowest_price:
                return True

    return False
