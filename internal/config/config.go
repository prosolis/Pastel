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

	return c, nil
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

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
