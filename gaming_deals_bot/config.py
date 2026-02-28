import os


class Config:
    """Bot configuration loaded from environment variables."""

    def __init__(self):
        # Matrix settings
        self.matrix_homeserver_url = self._require("MATRIX_HOMESERVER_URL")
        self.matrix_bot_user_id = self._require("MATRIX_BOT_USER_ID")
        self.matrix_bot_access_token = self._require("MATRIX_BOT_ACCESS_TOKEN")
        self.matrix_deals_room_id = self._require("MATRIX_DEALS_ROOM_ID")

        # ITAD API key (optional — historical low checks disabled without it)
        self.itad_api_key = os.environ.get("ITAD_API_KEY", "")

        # Deal sources: comma-separated list of "cheapshark", "itad" (default: cheapshark)
        raw_sources = os.environ.get("DEAL_SOURCES", "cheapshark")
        self.deal_sources: list[str] = [
            s.strip().lower() for s in raw_sources.split(",") if s.strip()
        ]

        # Deal filtering
        self.min_deal_rating = float(os.environ.get("MIN_DEAL_RATING", "8.0"))
        self.min_discount_percent = int(os.environ.get("MIN_DISCOUNT_PERCENT", "50"))
        self.max_price_usd = float(os.environ.get("MAX_PRICE_USD", "20"))

        # Intro message on startup
        self.send_intro_message = os.environ.get(
            "SEND_INTRO_MESSAGE", "false"
        ).lower() in ("true", "1", "yes")

        # Database
        self.database_path = os.environ.get("DATABASE_PATH", "deals.db")

    @staticmethod
    def _require(name: str) -> str:
        value = os.environ.get(name)
        if not value:
            raise ValueError(f"Required environment variable {name} is not set")
        return value
