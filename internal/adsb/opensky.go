package adsb

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OpenSkyAircraft holds enriched aircraft data from OpenSky Network.
type OpenSkyAircraft struct {
	ICAO24         string `json:"icao24"`
	Registration   string `json:"registration,omitempty"`
	ManufacturerICAO string `json:"manufacturerIcao,omitempty"`
	ManufacturerName string `json:"manufacturerName,omitempty"`
	Model          string `json:"model,omitempty"`
	TypeCode       string `json:"typecode,omitempty"`
	Owner          string `json:"owner,omitempty"`
	Operator       string `json:"operator,omitempty"`
	OperatorICAO   string `json:"operatorIcao,omitempty"`
	Built          string `json:"built,omitempty"`
	CategoryDesc   string `json:"categoryDescription,omitempty"`
}

// OpenSkyClient provides aircraft metadata from the OpenSky Network API.
type OpenSkyClient struct {
	clientID     string
	clientSecret string
	tokenURL     string
	apiURL       string
	token        string
	tokenExpiry  time.Time
	cache        map[string]*OpenSkyAircraft
	cacheTTL     time.Duration
	cacheTime    map[string]time.Time
	mu           sync.Mutex
	client       *http.Client
}

// NewOpenSkyClient creates a new OpenSky Network API client.
func NewOpenSkyClient(clientID, clientSecret string) *OpenSkyClient {
	return &OpenSkyClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token",
		apiURL:       "https://opensky-network.org/api/metadata/aircraft/icao",
		cache:        make(map[string]*OpenSkyAircraft),
		cacheTTL:     60 * time.Minute,
		cacheTime:    make(map[string]time.Time),
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

// Lookup returns enriched aircraft data for an ICAO24 hex code.
func (c *OpenSkyClient) Lookup(icao24 string) (*OpenSkyAircraft, error) {
	icao24 = strings.ToLower(icao24)

	c.mu.Lock()
	// Check cache
	if cached, ok := c.cache[icao24]; ok {
		if time.Since(c.cacheTime[icao24]) < c.cacheTTL {
			c.mu.Unlock()
			return cached, nil
		}
	}
	c.mu.Unlock()

	// Authenticate if needed
	if err := c.ensureToken(); err != nil {
		return nil, fmt.Errorf("opensky auth failed: %w", err)
	}

	// Fetch metadata
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s", c.apiURL, icao24), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Unknown aircraft
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opensky API returned %d", resp.StatusCode)
	}

	var aircraft OpenSkyAircraft
	if err := json.NewDecoder(resp.Body).Decode(&aircraft); err != nil {
		return nil, err
	}

	// Cache result
	c.mu.Lock()
	c.cache[icao24] = &aircraft
	c.cacheTime[icao24] = time.Now()
	// Prune cache if too large
	if len(c.cache) > 1000 {
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

	return &aircraft, nil
}

// ensureToken fetches or refreshes the OAuth2 token using client credentials.
func (c *OpenSkyClient) ensureToken() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return nil
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	resp, err := c.client.PostForm(c.tokenURL, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}

	c.token = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-30) * time.Second) // 30s buffer

	slog.Info("OpenSky token acquired", "expires_in", tokenResp.ExpiresIn)
	return nil
}
