"""Matrix client wrapper for sending deal messages."""

import asyncio
import logging

from nio import AsyncClient, PresenceSetError, RoomSendError, RoomSendResponse

logger = logging.getLogger(__name__)

PRESENCE_HEARTBEAT_INTERVAL = 60  # seconds


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
        self._heartbeat_task: asyncio.Task | None = None

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

    async def send_notice(self, text: str) -> bool:
        """Send a plain m.notice (non-highlight) message to the configured room."""
        content = {
            "msgtype": "m.notice",
            "body": text,
        }

        try:
            resp = await self._client.room_send(
                room_id=self.room_id,
                message_type="m.room.message",
                content=content,
            )
            if isinstance(resp, RoomSendError):
                logger.error("Failed to send notice: %s", resp.message)
                return False
            return True
        except Exception as exc:
            logger.error("Matrix send error: %s", exc)
            return False

    async def send_deal_in_thread(
        self, plain_text: str, html: str, thread_root_id: str
    ) -> bool:
        """Send a deal message as a reply inside a Matrix thread.

        ``thread_root_id`` is the ``$event_id`` of the thread root message.
        """
        content = {
            "msgtype": "m.text",
            "format": "org.matrix.custom.html",
            "body": plain_text,
            "formatted_body": html,
            "m.relates_to": {
                "rel_type": "m.thread",
                "event_id": thread_root_id,
                # Fallback for clients that don't support threads
                "is_falling_back": True,
                "m.in_reply_to": {"event_id": thread_root_id},
            },
        }

        try:
            resp = await self._client.room_send(
                room_id=self.room_id,
                message_type="m.room.message",
                content=content,
            )
            if isinstance(resp, RoomSendError):
                logger.error("Failed to send threaded message: %s", resp.message)
                return False
            logger.debug("Threaded message sent: %s (thread %s)", resp.event_id, thread_root_id)
            return True
        except Exception as exc:
            logger.error("Matrix threaded send error: %s", exc)
            return False

    async def create_thread_root(self, plain_text: str, html: str) -> str | None:
        """Send a message that will serve as a thread root.

        Returns the ``$event_id`` on success, or ``None`` on failure.
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
                logger.error("Failed to create thread root: %s", resp.message)
                return None
            assert isinstance(resp, RoomSendResponse)
            logger.info("Thread root created: %s", resp.event_id)
            return resp.event_id
        except Exception as exc:
            logger.error("Matrix thread root error: %s", exc)
            return None

    async def set_presence(self, presence: str) -> bool:
        """Set the bot's presence status on the homeserver.

        Returns True on success, False on failure.
        """
        try:
            resp = await self._client.set_presence(presence)
            if isinstance(resp, PresenceSetError):
                logger.error("Failed to set presence to %s: %s", presence, resp.message)
                return False
            logger.debug("Presence set to %s", presence)
            return True
        except Exception as exc:
            logger.error("Presence set error: %s", exc)
            return False

    async def start_presence_heartbeat(self):
        """Set presence to online and spawn a background task to keep it alive."""
        await self.set_presence("online")
        self._heartbeat_task = asyncio.create_task(self._presence_heartbeat_loop())
        logger.info("Presence heartbeat started (every %ds)", PRESENCE_HEARTBEAT_INTERVAL)

    async def _presence_heartbeat_loop(self):
        """Re-send 'online' presence every PRESENCE_HEARTBEAT_INTERVAL seconds."""
        try:
            while True:
                await asyncio.sleep(PRESENCE_HEARTBEAT_INTERVAL)
                await self.set_presence("online")
        except asyncio.CancelledError:
            pass

    async def stop_presence_heartbeat(self):
        """Cancel the heartbeat task and set presence to offline."""
        if self._heartbeat_task is not None:
            self._heartbeat_task.cancel()
            try:
                await self._heartbeat_task
            except asyncio.CancelledError:
                pass
            self._heartbeat_task = None
        await self.set_presence("offline")
        logger.info("Presence heartbeat stopped, set to offline")

    async def close(self):
        await self.stop_presence_heartbeat()
        await self._client.close()
