"""Preflight checks — validate configuration and connectivity before running the bot."""

import logging
import sys

import httpx
from nio import AsyncClient, LoginError, RoomResolveAliasError

from .config import Config
from .cheapshark import BASE_URL as CHEAPSHARK_URL
from .currency import FRANKFURTER_URL, configure as configure_currency, _all_target_currencies
from .epic import FREE_GAMES_URL
from .itad import BASE_URL as ITAD_URL

logger = logging.getLogger(__name__)

# ANSI colours for terminal output
_GREEN = "\033[32m"
_RED = "\033[31m"
_YELLOW = "\033[33m"
_BOLD = "\033[1m"
_RESET = "\033[0m"


def _pass(label: str, detail: str = "") -> bool:
    suffix = f" — {detail}" if detail else ""
    print(f"  {_GREEN}✓{_RESET} {label}{suffix}")
    return True


def _fail(label: str, detail: str = "") -> bool:
    suffix = f" — {detail}" if detail else ""
    print(f"  {_RED}✗{_RESET} {label}{suffix}")
    return False


def _skip(label: str, detail: str = "") -> bool:
    suffix = f" — {detail}" if detail else ""
    print(f"  {_YELLOW}–{_RESET} {label}{suffix}")
    return True  # skips don't count as failures


async def run_preflight(config: Config) -> bool:
    """Run all preflight checks. Returns True if everything critical passes."""
    print(f"\n{_BOLD}Pastel — preflight checks{_RESET}\n")
    all_ok = True

    # Configure currency display so the Frankfurter check fetches the right symbols
    configure_currency(config.default_currency, config.extra_currencies)

    async with httpx.AsyncClient(timeout=15) as http:
        # --- Matrix ---
        print(f"{_BOLD}Matrix{_RESET}")
        all_ok &= await _check_matrix(config)

        # --- CheapShark ---
        print(f"\n{_BOLD}CheapShark{_RESET}")
        if "cheapshark" in config.deal_sources:
            all_ok &= await _check_cheapshark(http)
        else:
            _skip("Skipped", "not in DEAL_SOURCES")

        # --- Epic Games Store ---
        print(f"\n{_BOLD}Epic Games Store{_RESET}")
        all_ok &= await _check_epic(http)

        # --- Frankfurter (exchange rates) ---
        print(f"\n{_BOLD}Frankfurter (exchange rates){_RESET}")
        all_ok &= await _check_frankfurter(http, config)

        # --- IsThereAnyDeal ---
        itad_required = "itad" in config.deal_sources
        print(f"\n{_BOLD}IsThereAnyDeal{_RESET}")
        all_ok &= await _check_itad(http, config.itad_api_key, required=itad_required)

    # --- Summary ---
    print()
    if all_ok:
        print(f"{_GREEN}{_BOLD}All checks passed.{_RESET} The bot is ready to run.")
    else:
        print(f"{_RED}{_BOLD}Some checks failed.{_RESET} Review the errors above before starting the bot.")
    print()

    return all_ok


# ---------------------------------------------------------------------------
# Individual checks
# ---------------------------------------------------------------------------


async def _check_matrix(config: Config) -> bool:
    """Verify the access token is valid and the bot can see the target room."""
    ok = True
    client = AsyncClient(config.matrix_homeserver_url, config.matrix_bot_user_id)
    client.access_token = config.matrix_bot_access_token
    client.user_id = config.matrix_bot_user_id

    try:
        # whoami — validates the token
        resp = await client.whoami()
        if hasattr(resp, "user_id"):
            ok &= _pass("Authentication", f"logged in as {resp.user_id}")
        else:
            ok &= _fail("Authentication", f"token rejected: {resp}")
            await client.close()
            return False

        # joined_rooms — check that the bot is in the target room
        rooms_resp = await client.joined_rooms()
        if hasattr(rooms_resp, "rooms"):
            if config.matrix_deals_room_id in rooms_resp.rooms:
                ok &= _pass("Room access", f"bot is a member of {config.matrix_deals_room_id}")
            else:
                ok &= _fail(
                    "Room access",
                    f"bot is NOT in {config.matrix_deals_room_id} — invite the bot first",
                )
        else:
            ok &= _fail("Room access", f"could not list joined rooms: {rooms_resp}")
    except Exception as exc:
        ok &= _fail("Homeserver connection", str(exc))
    finally:
        await client.close()

    return ok


async def _check_cheapshark(http: httpx.AsyncClient) -> bool:
    """Hit the CheapShark deals endpoint to confirm it's reachable."""
    try:
        resp = await http.get(f"{CHEAPSHARK_URL}/deals", params={"pageSize": "1"})
        resp.raise_for_status()
        deals = resp.json()
        if isinstance(deals, list):
            return _pass("API reachable", f"{len(deals)} deal(s) in response")
        return _fail("API reachable", "unexpected response format")
    except Exception as exc:
        return _fail("API reachable", str(exc))


async def _check_epic(http: httpx.AsyncClient) -> bool:
    """Hit the Epic free-games endpoint."""
    try:
        resp = await http.get(FREE_GAMES_URL, params={"locale": "en-US"})
        resp.raise_for_status()
        data = resp.json()
        elements = (
            data.get("data", {})
            .get("Catalog", {})
            .get("searchStore", {})
            .get("elements", [])
        )
        return _pass("API reachable", f"{len(elements)} game(s) in catalog")
    except Exception as exc:
        return _fail("API reachable", str(exc))


async def _check_frankfurter(http: httpx.AsyncClient, config: Config) -> bool:
    """Fetch exchange rates to confirm Frankfurter is reachable."""
    needed = _all_target_currencies()
    if not needed:
        return _pass(
            "Skipped",
            f"only {config.default_currency} configured — no conversion needed",
        )
    try:
        symbols = ",".join(needed)
        resp = await http.get(FRANKFURTER_URL, params={"base": "USD", "symbols": symbols})
        resp.raise_for_status()
        rates = resp.json().get("rates", {})
        if rates:
            parts = [f"{k}: {v}" for k, v in rates.items()]
            return _pass("API reachable", ", ".join(parts))
        return _fail("API reachable", "response contained no rates")
    except Exception as exc:
        return _fail("API reachable", str(exc))


async def _check_itad(http: httpx.AsyncClient, api_key: str, *, required: bool = False) -> bool:
    """Verify the ITAD API key works.

    When *required* is True (ITAD is a deal source), a missing key is a failure.
    Otherwise it's an optional skip.
    """
    if not api_key:
        if required:
            return _fail("API key", "ITAD_API_KEY is required when 'itad' is in DEAL_SOURCES")
        return _skip("Skipped", "no ITAD_API_KEY configured (optional)")

    try:
        # Use the lookup endpoint (GET) to validate the key with a known game
        resp = await http.get(
            f"{ITAD_URL}/games/lookup/v1",
            params={"key": api_key, "appid": 220},  # Half-Life 2
        )
        if resp.status_code in (401, 403):
            return _fail("API key", "rejected by ITAD (401/403)")
        resp.raise_for_status()
        data = resp.json()
        if data.get("found"):
            return _pass("API reachable", "ITAD responded successfully")
        return _pass("API reachable", "ITAD key valid (game not found)")
    except Exception as exc:
        return _fail("API reachable", str(exc))
