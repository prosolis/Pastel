package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	MatrixHomeserverURL  string
	MatrixBotUserID      string
	MatrixBotAccessToken string
	MatrixBotPassword    string
	MatrixDealsRoomID    string
	ITADAPIKey           string
	DealSources          []string
	MinDealRating        float64
	MinDiscountPercent   int
	MaxPriceUSD          float64
	SendIntroMessage     bool
	DatabasePath         string

	// Web UI
	WebEnabled       bool
	WebListenAddr    string
	WebPublicURL     string
	MatrixServerName string
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
}

func Load() (*Config, error) {
	// Load .env file if present (ignore error if missing)
	_ = godotenv.Load()

	c := &Config{}
	var err error

	c.MatrixHomeserverURL, err = require("MATRIX_HOMESERVER_URL")
	if err != nil {
		return nil, err
	}
	c.MatrixBotUserID, err = require("MATRIX_BOT_USER_ID")
	if err != nil {
		return nil, err
	}
	c.MatrixBotAccessToken, err = require("MATRIX_BOT_ACCESS_TOKEN")
	if err != nil {
		return nil, err
	}
	c.MatrixBotPassword = os.Getenv("MATRIX_BOT_PASSWORD")

	c.MatrixDealsRoomID, err = require("MATRIX_DEALS_ROOM_ID")
	if err != nil {
		return nil, err
	}

	c.ITADAPIKey = os.Getenv("ITAD_API_KEY")

	rawSources := os.Getenv("DEAL_SOURCES")
	if rawSources == "" {
		rawSources = "cheapshark"
	}
	for _, s := range strings.Split(rawSources, ",") {
		s = strings.TrimSpace(strings.ToLower(s))
		if s != "" {
			c.DealSources = append(c.DealSources, s)
		}
	}

	c.MinDealRating = envFloat("MIN_DEAL_RATING", 7.0)
	c.MinDiscountPercent = envInt("MIN_DISCOUNT_PERCENT", 20)
	c.MaxPriceUSD = envFloat("MAX_PRICE_USD", 45)
	c.DatabasePath = envStr("DATABASE_PATH", "deals.db")

	intro := strings.ToLower(os.Getenv("SEND_INTRO_MESSAGE"))
	c.SendIntroMessage = intro == "true" || intro == "1" || intro == "yes"

	// Web UI configuration
	c.WebEnabled = envBool("WEB_ENABLED", false)
	c.WebListenAddr = envStr("WEB_LISTEN_ADDR", ":8080")
	c.WebPublicURL = os.Getenv("WEB_PUBLIC_URL")
	// MATRIX_SERVER_NAME defaults to the domain of the bot's Matrix user ID,
	// e.g. "@pastel:example.com" -> "example.com".
	c.MatrixServerName = envStr("MATRIX_SERVER_NAME", serverNameFromUserID(c.MatrixBotUserID))
	c.OIDCIssuerURL = os.Getenv("OIDC_ISSUER_URL")
	c.OIDCClientID = os.Getenv("OIDC_CLIENT_ID")
	c.OIDCClientSecret = os.Getenv("OIDC_CLIENT_SECRET")

	return c, nil
}

// serverNameFromUserID extracts the homeserver domain from a Matrix user ID
// like "@user:server.name". Returns "" if the ID has no domain part.
func serverNameFromUserID(userID string) string {
	if i := strings.IndexByte(userID, ':'); i >= 0 && i+1 < len(userID) {
		return userID[i+1:]
	}
	return ""
}

// HasSource checks if a deal source is enabled.
func (c *Config) HasSource(name string) bool {
	for _, s := range c.DealSources {
		if s == name {
			return true
		}
	}
	return false
}

func require(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return v, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	return v == "true" || v == "1" || v == "yes"
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
