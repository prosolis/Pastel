"""Main bot orchestration — ties together API clients, database, and Matrix posting."""

import asyncio
import logging

import httpx

from .cheapshark import CheapSharkDeal, fetch_deals as fetch_cheapshark_deals
from .config import Config
from .currency import configure as configure_currency, refresh_rates
from .database import Database
from .epic import EpicFreeGame, fetch_free_games
from .formatter import format_deal, format_epic_free, format_epic_upcoming, format_itad_deal
from .itad import check_single_historical_low
from .itad_deals import ITADDeal, fetch_deals as fetch_itad_deals
from .matrix_client import MatrixDealsClient
from .threads import ThreadCategory, format_thread_root, itad_type_to_category

logger = logging.getLogger(__name__)


class DealsBot:
    def __init__(self, config: Config):
        self.config = config
        self.db = Database(config.database_path)
        self.matrix = MatrixDealsClient(
            homeserver_url=config.matrix_homeserver_url,
            user_id=config.matrix_bot_user_id,
            access_token=config.matrix_bot_access_token,
            room_id=config.matrix_deals_room_id,
        )
        self._http = httpx.AsyncClient(timeout=30)
        self._first_run_done = False

    async def start(self):
        """Initialize database, fetch exchange rates, and run first-run population."""
        await self.db.connect()

        # Configure currency display before fetching rates
        configure_currency(self.config.default_currency, self.config.extra_currencies)

        # Pre-fetch exchange rates so the first deal post has conversions
        await refresh_rates(self._http)

        first_run = await self.db.get_config("first_run_done")
        if first_run != "true":
            logger.info("First run detected — populating database without posting")
            await self._populate_initial_state()
            await self.db.set_config("first_run_done", "true")
            self._first_run_done = True
        else:
            self._first_run_done = True

        # Start presence heartbeat so the bot shows as "online"
        await self.matrix.start_presence_heartbeat()

    async def stop(self):
        """Clean shutdown."""
        await self._http.aclose()
        await self.matrix.close()
        await self.db.close()

    async def _populate_initial_state(self):
        """Fetch current deals and record them without posting (avoids spam on first run).

        Epic free games are intentionally *not* recorded here so that the
        subsequent ``check_epic_free_games`` call will post them.  There are
        only a handful at any time and they are time-limited, so users should
        see them immediately rather than waiting for the next cycle.
        """
        total = 0

        if "cheapshark" in self.config.deal_sources:
            deals = await fetch_cheapshark_deals(
                self._http,
                max_price=self.config.cheapshark_max_price,
                min_rating=self.config.cheapshark_min_rating,
                min_discount=self.config.cheapshark_min_discount,
            )
            for deal in deals:
                await self.db.mark_posted(deal.dedup_id, "cheapshark", deal.title)
            total += len(deals)

        if "itad" in self.config.deal_sources and self.config.itad_api_key:
            itad_deals = await fetch_itad_deals(
                self._http,
                self.config.itad_api_key,
                countries=self.config.itad_countries,
                max_price=self.config.itad_max_price,
                min_discount=self.config.itad_min_discount,
                limit=self.config.itad_deals_limit,
            )
            for deal in itad_deals:
                await self.db.mark_posted(deal.dedup_id, "itad", deal.title)
            total += len(itad_deals)

        logger.info("First run: recorded %d existing deals", total)

    async def send_intro(self):
        """Send an intro message to the Matrix room if configured."""
        if not self.config.send_intro_message:
            return
        await self.matrix.send_notice("The deals must flow.")

    # ------------------------------------------------------------------
    # Thread helpers
    # ------------------------------------------------------------------

    async def _get_or_create_thread(self, category: ThreadCategory) -> str | None:
        """Return the event ID of the thread root for *category*, creating it if needed."""
        event_id = await self.db.get_thread_root(category.value)
        if event_id:
            return event_id

        plain_text, html = format_thread_root(category)
        event_id = await self.matrix.create_thread_root(plain_text, html)
        if event_id:
            await self.db.set_thread_root(category.value, event_id)
            logger.info("Created thread root for %s: %s", category.value, event_id)
        return event_id

    async def _send_to_thread_or_room(
        self, plain_text: str, html: str, category: ThreadCategory
    ) -> bool:
        """Send a message — into a thread if threads are enabled, otherwise to the room."""
        if not self.config.matrix_use_threads:
            return await self.matrix.send_deal(plain_text, html)

        thread_root_id = await self._get_or_create_thread(category)
        if not thread_root_id:
            logger.warning(
                "Could not obtain thread root for %s — falling back to room", category.value
            )
            return await self.matrix.send_deal(plain_text, html)

        return await self.matrix.send_deal_in_thread(plain_text, html, thread_root_id)

    # ------------------------------------------------------------------
    # CheapShark
    # ------------------------------------------------------------------

    async def check_cheapshark(self):
        """Poll CheapShark for deals and post new ones."""
        if not self._first_run_done:
            return
        if "cheapshark" not in self.config.deal_sources:
            return

        logger.info("Checking CheapShark for deals...")
        deals = await fetch_cheapshark_deals(
            self._http,
            max_price=self.config.cheapshark_max_price,
            min_rating=self.config.cheapshark_min_rating,
            min_discount=self.config.cheapshark_min_discount,
        )

        for deal in deals:
            await self._process_deal(deal)

        # Prune old records
        await self.db.prune_old(days=30)

    async def _process_deal(self, deal: CheapSharkDeal):
        """Check dedup, check historical low, format, and post a single deal."""
        if await self.db.has_been_posted(deal.dedup_id):
            return

        # Check if this is a historical low via ITAD
        is_historical_low = False
        if self.config.itad_api_key and deal.steam_app_id:
            is_historical_low = await check_single_historical_low(
                self._http,
                self.config.itad_api_key,
                deal.steam_app_id,
            )

        plain_text, html = format_deal(deal, is_historical_low)
        success = await self._send_to_thread_or_room(
            plain_text, html, ThreadCategory.GAME_DEALS
        )

        if success:
            await self.db.mark_posted(deal.dedup_id, "cheapshark", deal.title)
            logger.info("Posted deal: %s", deal.title)
        else:
            logger.warning("Failed to post deal: %s — will retry next cycle", deal.title)

    # ------------------------------------------------------------------
    # IsThereAnyDeal
    # ------------------------------------------------------------------

    async def check_itad_deals(self):
        """Poll IsThereAnyDeal for deals and post new ones."""
        if not self._first_run_done:
            return
        if "itad" not in self.config.deal_sources:
            return
        if not self.config.itad_api_key:
            logger.warning("ITAD deal source enabled but ITAD_API_KEY is not set")
            return

        logger.info("Checking IsThereAnyDeal for deals...")
        deals = await fetch_itad_deals(
            self._http,
            self.config.itad_api_key,
            countries=self.config.itad_countries,
            max_price=self.config.itad_max_price,
            min_discount=self.config.itad_min_discount,
            limit=self.config.itad_deals_limit,
        )

        for deal in deals:
            await self._process_itad_deal(deal)

        await self.db.prune_old(days=30)

    async def _process_itad_deal(self, deal: ITADDeal):
        """Check dedup, format, and post a single ITAD deal."""
        if await self.db.has_been_posted(deal.dedup_id):
            return

        category = itad_type_to_category(deal.deal_type)

        # When threads are off, preserve original behaviour: skip non-game content
        if not self.config.matrix_use_threads and category == ThreadCategory.NON_GAME_DEALS:
            logger.debug("Skipping non-game ITAD deal (threads disabled): %s", deal.title)
            return

        plain_text, html = format_itad_deal(deal)
        success = await self._send_to_thread_or_room(plain_text, html, category)

        if success:
            await self.db.mark_posted(deal.dedup_id, "itad", deal.title)
            logger.info("Posted ITAD deal: %s [%s]", deal.title, category.value)
        else:
            logger.warning("Failed to post ITAD deal: %s — will retry next cycle", deal.title)

    # ------------------------------------------------------------------
    # Epic Games Store
    # ------------------------------------------------------------------

    async def check_epic_free_games(self):
        """Poll Epic Games Store for free games and post new ones."""
        if not self._first_run_done:
            return

        logger.info("Checking Epic Games Store for free games...")
        current_free, upcoming = await fetch_free_games(self._http)

        for game in current_free:
            await self._process_epic_game(game, is_upcoming=False)

        for game in upcoming:
            await self._process_epic_game(game, is_upcoming=True)

    async def _process_epic_game(self, game: EpicFreeGame, *, is_upcoming: bool):
        """Check dedup, format, and post a single Epic free game."""
        if await self.db.has_been_posted(game.dedup_id):
            return

        if is_upcoming:
            plain_text, html = format_epic_upcoming(game)
        else:
            plain_text, html = format_epic_free(game)

        success = await self._send_to_thread_or_room(
            plain_text, html, ThreadCategory.EPIC_FREE
        )

        if success:
            await self.db.mark_posted(game.dedup_id, "epic", game.title)
            logger.info("Posted Epic game: %s (upcoming=%s)", game.title, is_upcoming)
        else:
            logger.warning(
                "Failed to post Epic game: %s — will retry next cycle", game.title
            )
