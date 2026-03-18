package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// DeviceInfo holds persisted device credentials for stable device identity
// across restarts.
type DeviceInfo struct {
	AccessToken string `json:"access_token"`
	DeviceID    string `json:"device_id"`
	UserID      string `json:"user_id"`
}

// ClientConfig holds the parameters needed to create a Matrix client.
type ClientConfig struct {
	HomeserverURL string
	UserID        string
	AccessToken   string
	Password      string // optional: enables auto-refresh and cross-signing
	CryptoDBPath  string // e.g. "crypto.db", empty to skip E2EE
	DevicePath    string // e.g. "device.json", empty to skip persistence
}

type Client struct {
	client     *mautrix.Client
	crypto     *cryptohelper.CryptoHelper
	cancel     context.CancelFunc
	syncCancel context.CancelFunc
	startTime  time.Time
	cfg        ClientConfig

	dmMu    sync.Mutex
	dmCache map[id.UserID]id.RoomID
}

// New creates a new Matrix client. If a password is provided, the client will:
//   - Persist device credentials to DevicePath for stable device identity
//   - Validate existing tokens on startup and re-login if expired
//   - Set LoginAs on the cryptohelper so it can auto-refresh mid-operation
//   - Bootstrap cross-signing so the bot's device is automatically verified
//
// If only an access token is provided (no password), the client works but
// cannot auto-refresh — the token must be manually replaced if it expires.
func New(cfg ClientConfig) (*Client, error) {
	uid := id.UserID(cfg.UserID)
	accessToken := cfg.AccessToken

	c := &Client{
		cfg:       cfg,
		dmCache:   make(map[id.UserID]id.RoomID),
		startTime: time.Now(),
	}

	// If we have a device file and password, try to load persisted credentials
	if cfg.DevicePath != "" && cfg.Password != "" {
		device, err := loadDevice(cfg.DevicePath)
		if err == nil && device.AccessToken != "" {
			if isTokenValid(cfg.HomeserverURL, device.AccessToken) {
				slog.Info("existing device credentials valid", "device_id", device.DeviceID)
				accessToken = device.AccessToken

				client, err := mautrix.NewClient(cfg.HomeserverURL, id.UserID(device.UserID), device.AccessToken)
				if err != nil {
					return nil, fmt.Errorf("create client with existing token: %w", err)
				}
				client.DeviceID = id.DeviceID(device.DeviceID)
				c.client = client
			} else {
				slog.Warn("existing device credentials expired, logging in again")
			}
		}
	}

	// If we don't have a valid client yet, try password login or fall back to token
	if c.client == nil && cfg.Password != "" {
		loginResp, err := loginWithPassword(cfg.HomeserverURL, cfg.UserID, cfg.Password)
		if err != nil {
			return nil, fmt.Errorf("login: %w", err)
		}

		client, err := mautrix.NewClient(cfg.HomeserverURL, id.UserID(loginResp.UserID), loginResp.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("create client after login: %w", err)
		}
		client.DeviceID = id.DeviceID(loginResp.DeviceID)
		c.client = client
		accessToken = loginResp.AccessToken

		// Save device credentials for next restart
		if cfg.DevicePath != "" {
			if err := saveDevice(cfg.DevicePath, &DeviceInfo{
				AccessToken: loginResp.AccessToken,
				DeviceID:    loginResp.DeviceID,
				UserID:      loginResp.UserID,
			}); err != nil {
				slog.Warn("failed to save device info", "error", err)
			}
		}

		slog.Info("logged in with password", "user_id", loginResp.UserID, "device_id", loginResp.DeviceID)
	}

	// Fall back to bare access token (no password, no auto-refresh)
	if c.client == nil {
		client, err := mautrix.NewClient(cfg.HomeserverURL, uid, accessToken)
		if err != nil {
			return nil, fmt.Errorf("create client: %w", err)
		}
		c.client = client
	}

	// Set up E2EE
	if cfg.CryptoDBPath != "" {
		ch, err := cryptohelper.NewCryptoHelper(c.client, []byte("pastel_pickle_key"), cfg.CryptoDBPath)
		if err != nil {
			return nil, fmt.Errorf("init crypto helper: %w", err)
		}

		// If password is available, enable auto-refresh on token expiry
		if cfg.Password != "" {
			ch.LoginAs = &mautrix.ReqLogin{
				Type: mautrix.AuthTypePassword,
				Identifier: mautrix.UserIdentifier{
					Type: mautrix.IdentifierTypeUser,
					User: cfg.UserID,
				},
				Password: cfg.Password,
			}
		}

		if err := ch.Init(context.Background()); err != nil {
			return nil, fmt.Errorf("crypto helper init: %w", err)
		}

		c.client.Crypto = ch
		c.crypto = ch

		// Bootstrap cross-signing if password is available
		if cfg.Password != "" {
			c.bootstrapCrossSigning()
		}

		slog.Info("E2EE initialized", "device_id", c.client.DeviceID)
	}

	return c, nil
}

func (c *Client) bootstrapCrossSigning() {
	mach := c.crypto.Machine()

	_, _, err := mach.GenerateAndUploadCrossSigningKeys(context.Background(), func(ui *mautrix.RespUserInteractive) interface{} {
		return map[string]interface{}{
			"type": mautrix.AuthTypePassword,
			"identifier": map[string]interface{}{
				"type": mautrix.IdentifierTypeUser,
				"user": c.cfg.UserID,
			},
			"password": c.cfg.Password,
			"session":  ui.Session,
		}
	}, "")
	if err != nil {
		slog.Debug("cross-signing: key upload skipped (may already exist)", "error", err)
	}

	if err := mach.SignOwnDevice(context.Background(), mach.OwnIdentity()); err != nil {
		slog.Debug("cross-signing: sign own device skipped", "error", err)
	}

	if err := mach.SignOwnMasterKey(context.Background()); err != nil {
		slog.Debug("cross-signing: sign master key skipped", "error", err)
	}
}

// RegisterMessageHandler registers a callback for incoming messages.
// Must be called before StartSync. Ignores the bot's own messages and
// messages from before the bot started (avoids replaying history).
func (c *Client) RegisterMessageHandler(fn func(senderID id.UserID, roomID id.RoomID, body string)) {
	syncer := c.client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		if evt.Sender == c.client.UserID {
			return
		}
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
func (c *Client) GetDMRoom(userID id.UserID) (id.RoomID, error) {
	c.dmMu.Lock()
	if roomID, ok := c.dmCache[userID]; ok {
		c.dmMu.Unlock()
		return roomID, nil
	}
	c.dmMu.Unlock()

	var dmRooms map[id.UserID][]id.RoomID
	err := c.client.GetAccountData(context.Background(), "m.direct", &dmRooms)
	if err == nil {
		if rooms, ok := dmRooms[userID]; ok && len(rooms) > 0 {
			roomID := rooms[len(rooms)-1]
			c.dmMu.Lock()
			c.dmCache[userID] = roomID
			c.dmMu.Unlock()
			return roomID, nil
		}
	}

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

// --- auth helpers ---

type loginResponse struct {
	AccessToken string `json:"access_token"`
	DeviceID    string `json:"device_id"`
	UserID      string `json:"user_id"`
}

func loginWithPassword(homeserver, user, password string) (*loginResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"type": "m.login.password",
		"identifier": map[string]string{
			"type": "m.id.user",
			"user": user,
		},
		"password": password,
	})

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Post(
		homeserver+"/_matrix/client/v3/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, respBody)
	}

	var result loginResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse login response: %w", err)
	}
	return &result, nil
}

func isTokenValid(homeserver, accessToken string) bool {
	req, err := http.NewRequest("GET", homeserver+"/_matrix/client/v3/account/whoami", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func loadDevice(path string) (*DeviceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info DeviceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func saveDevice(path string, info *DeviceInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
