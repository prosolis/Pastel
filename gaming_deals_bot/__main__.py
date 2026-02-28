"""Entry point — sets up logging, scheduler, and runs the bot."""

import asyncio
import logging
import signal
import sys

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from dotenv import load_dotenv

from .bot import DealsBot
from .config import Config
from .preflight import run_preflight


def setup_logging():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        stream=sys.stdout,
    )


async def main():
    load_dotenv()
    setup_logging()
    logger = logging.getLogger("gaming_deals_bot")

    check_mode = "--check" in sys.argv

    try:
        config = Config()
    except ValueError as exc:
        logger.error("Configuration error: %s", exc)
        sys.exit(1)

    if check_mode:
        ok = await run_preflight(config)
        sys.exit(0 if ok else 1)

    bot = DealsBot(config)
    await bot.start()

    scheduler = AsyncIOScheduler()

    # CheapShark: every 2 hours
    scheduler.add_job(
        bot.check_cheapshark,
        "interval",
        hours=2,
        id="cheapshark",
        name="CheapShark deals check",
    )

    # Epic free games: once daily
    scheduler.add_job(
        bot.check_epic_free_games,
        "interval",
        hours=24,
        id="epic_free",
        name="Epic free games check",
    )

    scheduler.start()
    logger.info("Bot started — scheduler running")

    # Run initial checks immediately (after first-run population is done)
    await bot.check_cheapshark()
    await bot.check_epic_free_games()

    # Keep running until signaled to stop
    stop_event = asyncio.Event()

    def handle_signal():
        logger.info("Shutdown signal received")
        stop_event.set()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, handle_signal)

    await stop_event.wait()

    logger.info("Shutting down...")
    scheduler.shutdown(wait=False)
    await bot.stop()
    logger.info("Bot stopped")


if __name__ == "__main__":
    asyncio.run(main())
