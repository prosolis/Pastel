"""Message formatting for Matrix deal posts.

Produces both a plain-text fallback and HTML formatted body for Matrix messages.
HTML is built directly using the Matrix-supported HTML subset rather than going
through a Markdown-to-HTML converter, which collapses line breaks.
"""

from datetime import datetime, timezone
from html import escape

from .cheapshark import CheapSharkDeal
from .currency import format_price
from .epic import EpicFreeGame
from .itad_deals import ITADDeal


def format_deal(deal: CheapSharkDeal, is_historical_low: bool = False) -> tuple[str, str]:
    """Format a CheapShark deal into (plain_text, html) for Matrix.

    Returns (body, formatted_body).
    """
    discount = int(deal.savings)
    sale_price = float(deal.sale_price)
    normal_price = float(deal.normal_price)

    sale_multi = format_price(sale_price)
    normal_display = format_price(normal_price)
    title = escape(deal.title)

    html_lines = [
        f"<strong>🎮 [DEAL] {title}</strong>",
        f"{discount}% off on {escape(deal.store_name)} <del>{escape(normal_display)}</del>",
        f"💰 <strong>{escape(sale_multi)}</strong>",
    ]
    if is_historical_low:
        html_lines.append("🏆 <em>All-time low!</em>")
    html_lines.append(f'🔗 <a href="{escape(deal.deal_url)}">View Deal</a>')
    html = "<br>\n".join(html_lines)

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


def format_itad_deal(deal: ITADDeal) -> tuple[str, str]:
    """Format an ITAD deal into (plain_text, html) for Matrix.

    Returns (body, formatted_body).
    """
    sale_multi = format_price(deal.sale_price)
    normal_display = format_price(deal.normal_price)
    title = escape(deal.title)

    html_lines = [
        f"<strong>🎮 [DEAL] {title}</strong>",
        f"{deal.discount}% off on {escape(deal.shop_name)} <del>{escape(normal_display)}</del>",
        f"💰 <strong>{escape(sale_multi)}</strong>",
    ]
    if deal.is_historical_low:
        html_lines.append("🏆 <em>All-time low!</em>")
    html_lines.append(f'🔗 <a href="{escape(deal.url)}">View Deal</a>')
    html = "<br>\n".join(html_lines)

    plain_lines = [
        f"🎮 [DEAL] {deal.title}",
        f"  {deal.discount}% off on {deal.shop_name} (was {normal_display})",
        f"  💰 {sale_multi}",
    ]
    if deal.is_historical_low:
        plain_lines.append("  🏆 All-time low!")
    plain_lines.append(f"  🔗 {deal.url}")
    plain_text = "\n".join(plain_lines)

    return plain_text, html


def format_epic_free(game: EpicFreeGame) -> tuple[str, str]:
    """Format an Epic free game into (plain_text, html) for Matrix.

    Returns (body, formatted_body).
    """
    title = escape(game.title)

    html_lines = [
        f"<strong>🆓 [FREE] {title}</strong>",
        "Free on Epic Games Store",
    ]

    if game.end_date:
        try:
            end_dt = datetime.fromisoformat(game.end_date.replace("Z", "+00:00"))
            date_str = end_dt.strftime("%B %-d")
            html_lines.append(f"📅 <em>Free until {date_str}</em>")
        except (ValueError, TypeError):
            pass

    if game.url:
        html_lines.append(f'🔗 <a href="{escape(game.url)}">Claim Now</a>')
    html = "<br>\n".join(html_lines)

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
    title = escape(game.title)

    html_lines = [
        f"<strong>📢 [UPCOMING FREE] {title}</strong>",
        "Coming soon — Free on Epic Games Store",
    ]

    if game.end_date:
        try:
            end_dt = datetime.fromisoformat(game.end_date.replace("Z", "+00:00"))
            date_str = end_dt.strftime("%B %-d")
            html_lines.append(f"📅 <em>Free until {date_str}</em>")
        except (ValueError, TypeError):
            pass

    if game.url:
        html_lines.append(f'🔗 <a href="{escape(game.url)}">Store Page</a>')
    html = "<br>\n".join(html_lines)

    plain_lines = [
        f"📢 [UPCOMING FREE] {game.title}",
        "  Coming soon — Free on Epic Games Store",
    ]
    if game.url:
        plain_lines.append(f"  🔗 {game.url}")
    plain_text = "\n".join(plain_lines)

    return plain_text, html
