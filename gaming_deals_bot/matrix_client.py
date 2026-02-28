"""Matrix client wrapper for sending deal messages."""

import logging

from nio import AsyncClient, RoomSendError

logger = logging.getLogger(__name__)


class MatrixDealsClient:
    def __init__(
        self,
        homeserver_url: str,
        user_id: str,
        access_token: str,
        room_id: str,
    ):
        self.room_id = room_id
        self._client = AsyncClient(homeserver_url, user_id)
        self._client.access_token = access_token
        self._client.user_id = user_id

    async def send_deal(self, plain_text: str, html: str) -> bool:
        """Send a deal message to the configured room.

        Returns True on success, False on failure.
        """
        content = {
            "msgtype": "m.text",
            "format": "org.matrix.custom.html",
            "body": plain_text,
            "formatted_body": html,
        }

        try:
            resp = await self._client.room_send(
                room_id=self.room_id,
                message_type="m.room.message",
                content=content,
            )
            if isinstance(resp, RoomSendError):
                logger.error("Failed to send message: %s", resp.message)
                return False
            logger.debug("Message sent: %s", resp.event_id)
            return True
        except Exception as exc:
            logger.error("Matrix send error: %s", exc)
            return False

    async def close(self):
        await self._client.close()
