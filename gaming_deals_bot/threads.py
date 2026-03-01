"""Thread category definitions for Matrix room threads.

When ``MATRIX_USE_THREADS=true``, deal messages are posted inside
per-category threads rather than directly into the room timeline.
"""

from enum import Enum
from html import escape


class ThreadCategory(str, Enum):
    """Categories that map to distinct Matrix threads."""

    GAME_DEALS = "game_deals"
    DLC_DEALS = "dlc_deals"
    EPIC_FREE = "epic_free"
    NON_GAME_DEALS = "non_game_deals"


# Display metadata for each thread category.
# ``label`` is the thread root title; ``description`` appears below it.
THREAD_META: dict[ThreadCategory, dict[str, str]] = {
    ThreadCategory.GAME_DEALS: {
        "label": "🎮 Game Deals",
        "description": "PC game deals from CheapShark and IsThereAnyDeal",
    },
    ThreadCategory.DLC_DEALS: {
        "label": "🧩 DLC Deals",
        "description": "DLC and expansion deals from IsThereAnyDeal",
    },
    ThreadCategory.EPIC_FREE: {
        "label": "🆓 Epic Free Games",
        "description": "Weekly free games from the Epic Games Store",
    },
    ThreadCategory.NON_GAME_DEALS: {
        "label": "📦 Non-Game Deals",
        "description": "Software, courses, and other non-game deals",
    },
}


def itad_type_to_category(deal_type: str) -> ThreadCategory:
    """Map an ITAD ``type`` value to a thread category."""
    if deal_type == "game":
        return ThreadCategory.GAME_DEALS
    if deal_type == "dlc":
        return ThreadCategory.DLC_DEALS
    return ThreadCategory.NON_GAME_DEALS


def format_thread_root(category: ThreadCategory) -> tuple[str, str]:
    """Return ``(plain_text, html)`` for a thread root message."""
    meta = THREAD_META[category]
    label = meta["label"]
    desc = meta["description"]

    plain_text = f"{label}\n{desc}"
    html = f"<strong>{escape(label)}</strong><br>\n<em>{escape(desc)}</em>"
    return plain_text, html
