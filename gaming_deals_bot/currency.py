"""Currency conversion using the Frankfurter API (ECB rates, no API key required).

Rates are cached in memory and refreshed at most once per hour.
"""

import logging
import time

import httpx

logger = logging.getLogger(__name__)

FRANKFURTER_URL = "https://api.frankfurter.dev/v1/latest"
TARGET_CURRENCIES = ("CAD", "EUR", "GBP")
CACHE_TTL_SECONDS = 3600  # 1 hour

# Module-level cache
_rates: dict[str, float] = {}
_last_fetched: float = 0.0


async def refresh_rates(client: httpx.AsyncClient) -> bool:
    """Fetch latest USD-based exchange rates from Frankfurter.

    Returns True if rates were successfully updated.
    """
    global _rates, _last_fetched

    symbols = ",".join(TARGET_CURRENCIES)
    try:
        resp = await client.get(
            FRANKFURTER_URL,
            params={"base": "USD", "symbols": symbols},
        )
        resp.raise_for_status()
        data = resp.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.warning("Failed to fetch exchange rates: %s", exc)
        return False

    new_rates = data.get("rates", {})
    if not new_rates:
        logger.warning("Frankfurter returned empty rates")
        return False

    _rates = {k: float(v) for k, v in new_rates.items()}
    _last_fetched = time.monotonic()
    logger.info("Exchange rates updated: %s", _rates)
    return True


async def _ensure_rates(client: httpx.AsyncClient) -> None:
    """Refresh rates if the cache is stale or empty."""
    if not _rates or (time.monotonic() - _last_fetched) > CACHE_TTL_SECONDS:
        await refresh_rates(client)


def _convert(usd_amount: float, currency: str) -> float | None:
    """Convert a USD amount to the target currency using cached rates."""
    rate = _rates.get(currency)
    if rate is None:
        return None
    return round(usd_amount * rate, 2)


# Currency display symbols
_SYMBOLS = {
    "USD": "$",
    "CAD": "C$",
    "EUR": "€",
    "GBP": "£",
}


def format_price(usd_amount: float) -> str:
    """Return a formatted multi-currency price string.

    Example: ``$14.99 · C$20.54 · €13.78 · £11.98``

    Falls back to USD-only if rates aren't available.
    """
    parts = [f"${usd_amount:.2f}"]

    for cur in TARGET_CURRENCIES:
        converted = _convert(usd_amount, cur)
        if converted is not None:
            symbol = _SYMBOLS.get(cur, cur)
            parts.append(f"{symbol}{converted:.2f}")

    return " · ".join(parts)


async def format_price_async(client: httpx.AsyncClient, usd_amount: float) -> str:
    """Ensure rates are fresh, then format a multi-currency price string."""
    await _ensure_rates(client)
    return format_price(usd_amount)
