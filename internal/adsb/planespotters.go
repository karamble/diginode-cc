package adsb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PlanespottersResult holds aircraft data from the Planespotters API.
type PlanespottersResult struct {
	ICAO24       string `json:"icao24"`
	Registration string `json:"registration,omitempty"`
	TypeCode     string `json:"typecode,omitempty"`
	Airline      string `json:"airline,omitempty"`
	PhotoURL     string `json:"photoUrl,omitempty"`
	PhotoCredit  string `json:"photoCredit,omitempty"`
}

// planespottersAPIResponse is the raw API response.
type planespottersAPIResponse struct {
	Photos []struct {
		Src         string `json:"src"`
		Link        string `json:"link"`
		Photographer string `json:"photographer"`
	} `json:"photos"`
}

// PlanespottersClient provides aircraft photo lookups.
type PlanespottersClient struct {
	apiURL    string
	cache     map[string]*PlanespottersResult
	cacheTime map[string]time.Time
	cacheTTL  time.Duration
	mu        sync.Mutex
	client    *http.Client
}

// NewPlanespottersClient creates a new Planespotters API client.
func NewPlanespottersClient() *PlanespottersClient {
	return &PlanespottersClient{
		apiURL:    "https://api.planespotters.net/pub/photos/hex",
		cache:     make(map[string]*PlanespottersResult),
		cacheTime: make(map[string]time.Time),
		cacheTTL:  60 * time.Minute,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Lookup returns aircraft photo data for an ICAO24 hex code.
func (c *PlanespottersClient) Lookup(icao24 string) (*PlanespottersResult, error) {
	icao24 = strings.ToUpper(icao24)

	c.mu.Lock()
	if cached, ok := c.cache[icao24]; ok {
		if time.Since(c.cacheTime[icao24]) < c.cacheTTL {
			c.mu.Unlock()
			return cached, nil
		}
	}
	c.mu.Unlock()

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s", c.apiURL, icao24), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "DigiNode-CC/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("planespotters API returned %d", resp.StatusCode)
	}

	var apiResp planespottersAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	result := &PlanespottersResult{
		ICAO24: icao24,
	}
	if len(apiResp.Photos) > 0 {
		result.PhotoURL = apiResp.Photos[0].Src
		result.PhotoCredit = apiResp.Photos[0].Photographer
	}

	// Cache
	c.mu.Lock()
	c.cache[icao24] = result
	c.cacheTime[icao24] = time.Now()
	if len(c.cache) > 500 {
		oldest := time.Now()
		var oldestKey string
		for k, t := range c.cacheTime {
			if t.Before(oldest) {
				oldest = t
				oldestKey = k
			}
		}
		delete(c.cache, oldestKey)
		delete(c.cacheTime, oldestKey)
	}
	c.mu.Unlock()

	return result, nil
}
