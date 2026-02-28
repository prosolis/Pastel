"""Main bot orchestration — ties together API clients, database, and Matrix posting."""

import asyncio
import logging

import httpx

from .cheapshark import CheapSharkDeal, fetch_deals
from .config import Config
from .database import Database
from .epic import EpicFreeGame, fetch_free_games
from .formatter import format_deal, format_epic_free, format_epic_upcoming
from .itad import check_single_historical_low
from .matrix_client import MatrixDealsClient

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
        """Initialize database and run first-run population."""
        await self.db.connect()

        first_run = await self.db.get_config("first_run_done")
        if first_run != "true":
            logger.info("First run detected — populating database without posting")
            await self._populate_initial_state()
            await self.db.set_config("first_run_done", "true")
            self._first_run_done = True
        else:
            self._first_run_done = True

    async def stop(self):
        """Clean shutdown."""
        await self._http.aclose()
        await self.matrix.close()
        await self.db.close()

    async def _populate_initial_state(self):
        """Fetch current deals and record them without posting (avoids spam on first run)."""
        deals = await fetch_deals(
            self._http,
            max_price=self.config.max_price_usd,
            min_rating=self.config.min_deal_rating,
            min_discount=self.config.min_discount_percent,
        )
        for deal in deals:
            await self.db.mark_posted(deal.dedup_id, "cheapshark", deal.title)

        current_free, upcoming = await fetch_free_games(self._http)
        for game in current_free:
            await self.db.mark_posted(game.dedup_id, "epic", game.title)
        for game in upcoming:
            await self.db.mark_posted(game.dedup_id, "epic", game.title)

        total = len(deals) + len(current_free) + len(upcoming)
        logger.info("First run: recorded %d existing deals/games", total)

    async def check_cheapshark(self):
        """Poll CheapShark for deals and post new ones."""
        if not self._first_run_done:
            return

        logger.info("Checking CheapShark for deals...")
        deals = await fetch_deals(
            self._http,
            max_price=self.config.max_price_usd,
            min_rating=self.config.min_deal_rating,
            min_discount=self.config.min_discount_percent,
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
        success = await self.matrix.send_deal(plain_text, html)

        if success:
            await self.db.mark_posted(deal.dedup_id, "cheapshark", deal.title)
            logger.info("Posted deal: %s", deal.title)
        else:
            logger.warning("Failed to post deal: %s — will retry next cycle", deal.title)

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

        success = await self.matrix.send_deal(plain_text, html)

        if success:
            await self.db.mark_posted(game.dedup_id, "epic", game.title)
            logger.info("Posted Epic game: %s (upcoming=%s)", game.title, is_upcoming)
        else:
            logger.warning(
                "Failed to post Epic game: %s — will retry next cycle", game.title
            )
