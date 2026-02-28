"""Currency conversion using the Frankfurter API (ECB rates, no API key required).

Rates are cached in memory and refreshed at most twice per day.
Call ``configure()`` before ``refresh_rates()`` to set the display currencies.
"""

import logging
import time

import httpx

logger = logging.getLogger(__name__)

FRANKFURTER_URL = "https://api.frankfurter.dev/v1/latest"
CACHE_TTL_SECONDS = 43200  # 12 hours — be nice to the free service

# Module-level state -------------------------------------------------------
_rates: dict[str, float] = {}
_last_fetched: float = 0.0

# Display preferences (set via configure())
_default_currency: str = "USD"
_extra_currencies: list[str] = ["CAD", "EUR", "GBP"]

# Currency display symbols
SYMBOLS: dict[str, str] = {
    "USD": "$",
    "CAD": "C$",
    "EUR": "€",
    "GBP": "£",
    "AUD": "A$",
    "BRL": "R$",
    "CHF": "CHF ",
    "CNY": "¥",
    "CZK": "Kč",
    "DKK": "kr",
    "HUF": "Ft",
    "INR": "₹",
    "JPY": "¥",
    "KRW": "₩",
    "MXN": "MX$",
    "NOK": "kr",
    "NZD": "NZ$",
    "PLN": "zł",
    "SEK": "kr",
    "TRY": "₺",
    "ZAR": "R",
}


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

def configure(default_currency: str = "USD", extra_currencies: list[str] | None = None) -> None:
    """Set the primary display currency and additional currencies.

    Must be called **before** ``refresh_rates()`` so the correct symbols are
    requested from Frankfurter.
    """
    global _default_currency, _extra_currencies
    _default_currency = default_currency.upper()
    if extra_currencies is not None:
        _extra_currencies = [c.upper() for c in extra_currencies]


def _all_target_currencies() -> list[str]:
    """Return every non-USD currency we need exchange rates for."""
    currencies: set[str] = set()
    if _default_currency != "USD":
        currencies.add(_default_currency)
    for c in _extra_currencies:
        if c != "USD":
            currencies.add(c)
    return sorted(currencies)


# ---------------------------------------------------------------------------
# Rate fetching
# ---------------------------------------------------------------------------

async def refresh_rates(client: httpx.AsyncClient) -> bool:
    """Fetch latest USD-based exchange rates from Frankfurter.

    Returns True if rates were successfully updated.
    """
    global _rates, _last_fetched

    needed = _all_target_currencies()
    if not needed:
        # Only USD display — no conversion needed
        _last_fetched = time.monotonic()
        return True

    symbols = ",".join(needed)
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
    if _all_target_currencies() and (
        not _rates or (time.monotonic() - _last_fetched) > CACHE_TTL_SECONDS
    ):
        await refresh_rates(client)


# ---------------------------------------------------------------------------
# Conversion helpers
# ---------------------------------------------------------------------------

def _convert_from_usd(usd_amount: float, currency: str) -> float | None:
    """Convert a USD amount to *currency* using cached rates."""
    if currency == "USD":
        return usd_amount
    rate = _rates.get(currency)
    if rate is None:
        return None
    return round(usd_amount * rate, 2)


def convert_to_usd(amount: float, source_currency: str) -> float:
    """Convert *amount* from *source_currency* to USD using cached rates.

    Falls back to a 1:1 conversion with a warning when rates are unavailable.
    """
    if source_currency == "USD":
        return amount
    rate = _rates.get(source_currency)
    if rate is None or rate == 0:
        logger.warning("No rate for %s → USD, assuming 1:1", source_currency)
        return amount
    return round(amount / rate, 2)


# ---------------------------------------------------------------------------
# Price formatting
# ---------------------------------------------------------------------------

def format_price(usd_amount: float) -> str:
    """Return a formatted multi-currency price string.

    The configured *default_currency* is shown first, followed by each
    *extra_currency*.  Example with defaults::

        $14.99 · C$20.54 · €13.78 · £11.98

    Falls back to USD-only if rates aren't available.
    """
    display_order = [_default_currency] + [
        c for c in _extra_currencies if c != _default_currency
    ]

    parts: list[str] = []
    for cur in display_order:
        converted = _convert_from_usd(usd_amount, cur)
        if converted is not None:
            symbol = SYMBOLS.get(cur, f"{cur} ")
            parts.append(f"{symbol}{converted:.2f}")

    if not parts:
        # Fallback when no rates are loaded yet
        parts = [f"${usd_amount:.2f}"]

    return " · ".join(parts)


async def format_price_async(client: httpx.AsyncClient, usd_amount: float) -> str:
    """Ensure rates are fresh, then format a multi-currency price string."""
    await _ensure_rates(client)
    return format_price(usd_amount)
