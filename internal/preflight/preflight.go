package preflight

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/prosolis/Pastel/internal/config"
	"github.com/prosolis/Pastel/internal/matrix"
)

type Result struct {
	Name   string
	Status string // "pass", "fail", "skip"
	Detail string
}

// Run executes all preflight checks and returns results.
func Run(cfg *config.Config) []Result {
	var results []Result

	results = append(results, checkMatrix(cfg))
	results = append(results, checkFrankfurter())

	if cfg.HasSource("cheapshark") {
		results = append(results, checkCheapShark())
	} else {
		results = append(results, Result{"CheapShark", "skip", "not in DEAL_SOURCES"})
	}

	results = append(results, checkEpic())

	if cfg.ITADAPIKey != "" || cfg.HasSource("itad") {
		results = append(results, checkITAD(cfg))
	} else {
		results = append(results, Result{"IsThereAnyDeal", "skip", "no API key and not in DEAL_SOURCES"})
	}

	return results
}

// PrintResults prints preflight results and returns true if all passed.
func PrintResults(results []Result) bool {
	allPass := true
	for _, r := range results {
		var icon string
		switch r.Status {
		case "pass":
			icon = "✓"
		case "fail":
			icon = "✗"
			allPass = false
		case "skip":
			icon = "–"
		}
		fmt.Printf("  %s %s: %s\n", icon, r.Name, r.Detail)
	}
	return allPass
}

func checkMatrix(cfg *config.Config) Result {
	client, err := matrix.New(cfg.MatrixHomeserverURL, cfg.MatrixBotUserID, cfg.MatrixBotAccessToken, "")
	if err != nil {
		return Result{"Matrix", "fail", fmt.Sprintf("client error: %v", err)}
	}

	who, err := client.Whoami()
	if err != nil {
		return Result{"Matrix", "fail", fmt.Sprintf("whoami failed: %v", err)}
	}

	rooms, err := client.JoinedRooms()
	if err != nil {
		return Result{"Matrix", "fail", fmt.Sprintf("joined_rooms failed: %v", err)}
	}

	inRoom := false
	for _, r := range rooms {
		if r == cfg.MatrixDealsRoomID {
			inRoom = true
			break
		}
	}

	if !inRoom {
		return Result{"Matrix", "fail", fmt.Sprintf("bot %s is not in room %s", who, cfg.MatrixDealsRoomID)}
	}

	return Result{"Matrix", "pass", fmt.Sprintf("authenticated as %s, in target room", who)}
}

func checkCheapShark() Result {
	resp, err := http.Get("https://www.cheapshark.com/api/1.0/deals?pageSize=1")
	if err != nil {
		return Result{"CheapShark", "fail", fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	var deals []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&deals); err != nil || len(deals) == 0 {
		return Result{"CheapShark", "fail", "unexpected response format"}
	}

	return Result{"CheapShark", "pass", "API reachable"}
}

func checkEpic() Result {
	resp, err := http.Get("https://store-site-backend-static.ak.epicgames.com/freeGamesPromotions?locale=en-US")
	if err != nil {
		return Result{"Epic Games", "fail", fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Catalog struct {
				SearchStore struct {
					Elements []json.RawMessage `json:"elements"`
				} `json:"searchStore"`
			} `json:"Catalog"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Result{"Epic Games", "fail", "unexpected response format"}
	}

	return Result{"Epic Games", "pass", fmt.Sprintf("API reachable, %d elements", len(result.Data.Catalog.SearchStore.Elements))}
}

func checkFrankfurter() Result {
	resp, err := http.Get("https://api.frankfurter.dev/v1/latest?base=USD&symbols=CAD,EUR,GBP")
	if err != nil {
		return Result{"Frankfurter", "fail", fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	var result struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Rates) == 0 {
		return Result{"Frankfurter", "fail", "no rates returned"}
	}

	return Result{"Frankfurter", "pass", fmt.Sprintf("rates: %v", result.Rates)}
}

func checkITAD(cfg *config.Config) Result {
	url := fmt.Sprintf("https://api.isthereanydeal.com/games/lookup/v1?key=%s&appid=220", cfg.ITADAPIKey)
	resp, err := http.Get(url)
	if err != nil {
		return Result{"IsThereAnyDeal", "fail", fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Result{"IsThereAnyDeal", "fail", "invalid API key"}
	}

	return Result{"IsThereAnyDeal", "pass", "API key valid"}
}
