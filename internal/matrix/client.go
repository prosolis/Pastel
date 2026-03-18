package matrix

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Client struct {
	client     *mautrix.Client
	crypto     *cryptohelper.CryptoHelper
	cancel     context.CancelFunc
	syncCancel context.CancelFunc
	hasE2EE    bool
	startTime  time.Time

	dmMu    sync.Mutex
	dmCache map[id.UserID]id.RoomID // userID -> DM room
}

// New creates a new Matrix client with E2EE support via cryptohelper.
// The cryptoDBPath is the path to the SQLite database for the crypto store
// (e.g. "crypto.db"). Call RegisterMessageHandler before StartSync to
// receive DM events.
func New(homeserverURL, userID, accessToken, cryptoDBPath string) (*Client, error) {
	uid := id.UserID(userID)
	client, err := mautrix.NewClient(homeserverURL, uid, accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create matrix client: %w", err)
	}

	c := &Client{
		client:    client,
		dmCache:   make(map[id.UserID]id.RoomID),
		startTime: time.Now(),
	}

	if cryptoDBPath != "" {
		ch, err := cryptohelper.NewCryptoHelper(client, []byte("pastel_pickle_key"), cryptoDBPath)
		if err != nil {
			return nil, fmt.Errorf("init crypto helper: %w", err)
		}

		if err := ch.Init(context.Background()); err != nil {
			return nil, fmt.Errorf("crypto helper init: %w", err)
		}

		client.Crypto = ch
		c.crypto = ch

		slog.Info("E2EE initialized", "device_id", client.DeviceID)
	}

	return c, nil
}

// RegisterMessageHandler registers a callback for incoming messages.
// Must be called before StartSync. Ignores the bot's own messages and
// messages from before the bot started (avoids replaying history).
func (c *Client) RegisterMessageHandler(fn func(senderID id.UserID, roomID id.RoomID, body string)) {
	syncer := c.client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		// Ignore own messages
		if evt.Sender == c.client.UserID {
			return
		}
		// Ignore messages from before bot startup (history replay)
		evtTime := time.UnixMilli(evt.Timestamp)
		if evtTime.Before(c.startTime) {
			return
		}
		msg := evt.Content.AsMessage()
		if msg == nil || msg.Body == "" {
			return
		}
		fn(evt.Sender, evt.RoomID, msg.Body)
	})
}

// StartSync launches the background sync loop. Must be called after handler
// registration so events are dispatched to registered callbacks.
func (c *Client) StartSync() {
	syncCtx, syncCancel := context.WithCancel(context.Background())
	c.syncCancel = syncCancel
	go func() {
		for {
			if err := c.client.SyncWithContext(syncCtx); err != nil {
				if syncCtx.Err() != nil {
					return
				}
				slog.Warn("sync error, retrying in 10s", "error", err)
				time.Sleep(10 * time.Second)
			}
		}
	}()
}

// Whoami validates the access token by calling /whoami.
func (c *Client) Whoami() (string, error) {
	resp, err := c.client.Whoami(context.Background())
	if err != nil {
		return "", err
	}
	return resp.UserID.String(), nil
}

// JoinedRooms returns the list of rooms the bot has joined.
func (c *Client) JoinedRooms() ([]string, error) {
	resp, err := c.client.JoinedRooms(context.Background())
	if err != nil {
		return nil, err
	}
	var rooms []string
	for _, r := range resp.JoinedRooms {
		rooms = append(rooms, r.String())
	}
	return rooms, nil
}

// SendDeal sends a formatted deal message to a room.
func (c *Client) SendDeal(roomID, plainText, html string) error {
	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          plainText,
		Format:        event.FormatHTML,
		FormattedBody: html,
	}
	_, err := c.client.SendMessageEvent(context.Background(), id.RoomID(roomID), event.EventMessage, content)
	if err != nil {
		return fmt.Errorf("failed to send deal message: %w", err)
	}
	return nil
}

// SendNotice sends a notice message to a room (non-highlighting).
func (c *Client) SendNotice(roomID, text string) error {
	content := &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    text,
	}
	_, err := c.client.SendMessageEvent(context.Background(), id.RoomID(roomID), event.EventMessage, content)
	if err != nil {
		return fmt.Errorf("failed to send notice: %w", err)
	}
	return nil
}

// GetDMRoom returns the DM room for a user, reusing an existing one if available.
// Checks the in-memory cache first, then m.direct account data, and only creates
// a new encrypted room as a last resort. Follows gogobee's pattern to avoid
// opening duplicate DM rooms against the same user.
func (c *Client) GetDMRoom(userID id.UserID) (id.RoomID, error) {
	c.dmMu.Lock()
	if roomID, ok := c.dmCache[userID]; ok {
		c.dmMu.Unlock()
		return roomID, nil
	}
	c.dmMu.Unlock()

	// Check m.direct account data for existing DM rooms
	var dmRooms map[id.UserID][]id.RoomID
	err := c.client.GetAccountData(context.Background(), "m.direct", &dmRooms)
	if err == nil {
		if rooms, ok := dmRooms[userID]; ok && len(rooms) > 0 {
			roomID := rooms[len(rooms)-1] // use most recent
			c.dmMu.Lock()
			c.dmCache[userID] = roomID
			c.dmMu.Unlock()
			return roomID, nil
		}
	}

	// No existing DM room — create an encrypted one
	resp, err := c.client.CreateRoom(context.Background(), &mautrix.ReqCreateRoom{
		Preset:   "trusted_private_chat",
		Invite:   []id.UserID{userID},
		IsDirect: true,
		InitialState: []*event.Event{
			{
				Type: event.StateEncryption,
				Content: event.Content{
					Parsed: &event.EncryptionEventContent{
						Algorithm: id.AlgorithmMegolmV1,
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create DM room: %w", err)
	}

	c.dmMu.Lock()
	c.dmCache[userID] = resp.RoomID
	c.dmMu.Unlock()

	slog.Info("created DM room", "user", userID, "room", resp.RoomID)
	return resp.RoomID, nil
}

// SendDM sends a direct message to a user. Reuses existing DM room if available.
func (c *Client) SendDM(userID id.UserID, text string) error {
	roomID, err := c.GetDMRoom(userID)
	if err != nil {
		return err
	}
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	}
	_, err = c.client.SendMessageEvent(context.Background(), roomID, event.EventMessage, content)
	return err
}

// StartPresenceHeartbeat starts a goroutine that sends online presence every 60 seconds.
func (c *Client) StartPresenceHeartbeat() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		// Send immediately
		c.sendPresence()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.sendPresence()
			}
		}
	}()
}

func (c *Client) sendPresence() {
	err := c.client.SetPresence(context.Background(), mautrix.ReqPresence{Presence: event.PresenceOnline})
	if err != nil {
		slog.Warn("failed to set presence", "error", err)
	}
}

// Stop cancels the presence heartbeat and sync loop.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.syncCancel != nil {
		c.syncCancel()
	}
	if c.crypto != nil {
		c.crypto.Close()
	}
}
