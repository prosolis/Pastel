"""Message formatting for Matrix deal posts.

Produces both a plain-text fallback and HTML formatted body for Matrix messages.
"""

from datetime import datetime, timezone

import markdown

from .cheapshark import CheapSharkDeal
from .currency import format_price
from .epic import EpicFreeGame

_md = markdown.Markdown()


def _render_html(md_text: str) -> str:
    """Convert Markdown to HTML, resetting the parser between calls."""
    _md.reset()
    return _md.convert(md_text)


def format_deal(deal: CheapSharkDeal, is_historical_low: bool = False) -> tuple[str, str]:
    """Format a CheapShark deal into (plain_text, html) for Matrix.

    Returns (body, formatted_body).
    """
    discount = int(deal.savings)
    sale_price = float(deal.sale_price)
    normal_price = float(deal.normal_price)

    sale_multi = format_price(sale_price)
    normal_display = format_price(normal_price)

    lines = [
        f"**🎮 [DEAL] {deal.title}**",
        f"> {discount}% off on {deal.store_name} ~~{normal_display}~~",
        f"> 💰 **{sale_multi}**",
    ]

    if is_historical_low:
        lines.append("> 🏆 _All-time low!_")

    lines.append(f"> 🔗 [View Deal]({deal.deal_url})")

    md_text = "\n".join(lines)
    html = _render_html(md_text)

    # Plain text fallback — strip markdown syntax
    plain_lines = [
        f"🎮 [DEAL] {deal.title}",
        f"  {discount}% off on {deal.store_name} (was {normal_display})",
        f"  💰 {sale_multi}",
    ]
    if is_historical_low:
        plain_lines.append("  🏆 All-time low!")
    plain_lines.append(f"  🔗 {deal.deal_url}")

    plain_text = "\n".join(plain_lines)

    return plain_text, html


def format_epic_free(game: EpicFreeGame) -> tuple[str, str]:
    """Format an Epic free game into (plain_text, html) for Matrix.

    Returns (body, formatted_body).
    """
    lines = [
        f"**🆓 [FREE] {game.title}**",
        "> Free on Epic Games Store",
    ]

    if game.end_date:
        try:
            end_dt = datetime.fromisoformat(game.end_date.replace("Z", "+00:00"))
            date_str = end_dt.strftime("%B %-d")
            lines.append(f"> 📅 _Free until {date_str}_")
        except (ValueError, TypeError):
            pass

    if game.url:
        lines.append(f"> 🔗 [Claim Now]({game.url})")

    md_text = "\n".join(lines)
    html = _render_html(md_text)

    # Plain text fallback
    plain_lines = [
        f"🆓 [FREE] {game.title}",
        "  Free on Epic Games Store",
    ]
    if game.end_date:
        try:
            end_dt = datetime.fromisoformat(game.end_date.replace("Z", "+00:00"))
            date_str = end_dt.strftime("%B %-d")
            plain_lines.append(f"  📅 Free until {date_str}")
        except (ValueError, TypeError):
            pass
    if game.url:
        plain_lines.append(f"  🔗 {game.url}")

    plain_text = "\n".join(plain_lines)

    return plain_text, html


def format_epic_upcoming(game: EpicFreeGame) -> tuple[str, str]:
    """Format an upcoming Epic free game into (plain_text, html) for Matrix."""
    lines = [
        f"**📢 [UPCOMING FREE] {game.title}**",
        "> Coming soon — Free on Epic Games Store",
    ]

    if game.end_date:
        try:
            end_dt = datetime.fromisoformat(game.end_date.replace("Z", "+00:00"))
            date_str = end_dt.strftime("%B %-d")
            lines.append(f"> 📅 _Free until {date_str}_")
        except (ValueError, TypeError):
            pass

    if game.url:
        lines.append(f"> 🔗 [Store Page]({game.url})")

    md_text = "\n".join(lines)
    html = _render_html(md_text)

    plain_lines = [
        f"📢 [UPCOMING FREE] {game.title}",
        "  Coming soon — Free on Epic Games Store",
    ]
    if game.url:
        plain_lines.append(f"  🔗 {game.url}")

    plain_text = "\n".join(plain_lines)

    return plain_text, html
