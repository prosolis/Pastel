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

        # ITAD countries: comma-separated ISO 3166-1 alpha-2 country codes
        # Deals are fetched for each country and merged (first country has priority
        # when the same game appears in multiple regions).
        raw_countries = os.environ.get("ITAD_COUNTRIES", "US")
        self.itad_countries: list[str] = [
            c.strip().upper() for c in raw_countries.split(",") if c.strip()
        ]

        # Currency display
        self.default_currency = os.environ.get("DEFAULT_CURRENCY", "USD").upper()
        raw_extra = os.environ.get("EXTRA_CURRENCIES", "CAD,EUR,GBP")
        self.extra_currencies: list[str] = [
            c.strip().upper() for c in raw_extra.split(",") if c.strip()
        ]

        # Shared filter fallbacks (support legacy env vars)
        _max_price = os.environ.get(
            "MAX_PRICE", os.environ.get("MAX_PRICE_USD", "20")
        )
        _min_discount = os.environ.get("MIN_DISCOUNT_PERCENT", "50")
        _min_rating = os.environ.get("MIN_DEAL_RATING", "8.0")

        # CheapShark-specific filtering (falls back to shared values)
        self.cheapshark_min_discount = int(
            os.environ.get("CHEAPSHARK_MIN_DISCOUNT", _min_discount)
        )
        self.cheapshark_min_rating = float(
            os.environ.get("CHEAPSHARK_MIN_RATING", _min_rating)
        )
        self.cheapshark_max_price = float(
            os.environ.get("CHEAPSHARK_MAX_PRICE", _max_price)
        )

        # ITAD-specific filtering (falls back to shared values)
        self.itad_min_discount = int(
            os.environ.get("ITAD_MIN_DISCOUNT", _min_discount)
        )
        self.itad_max_price = float(
            os.environ.get("ITAD_MAX_PRICE", _max_price)
        )
        self.itad_deals_limit = int(os.environ.get("ITAD_DEALS_LIMIT", "200"))

        # Matrix threads — post deals into per-category threads
        self.matrix_use_threads = os.environ.get(
            "MATRIX_USE_THREADS", "false"
        ).lower() in ("true", "1", "yes")

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
