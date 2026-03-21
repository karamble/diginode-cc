package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MQTTSiteConfig represents an MQTT site configuration.
type MQTTSiteConfig struct {
	SiteID    string `json:"siteId"`
	BrokerURL string `json:"brokerUrl"`
	Topic     string `json:"topic,omitempty"`
	Enabled   bool   `json:"enabled"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
}

// MQTTSiteStatus represents the connection status of an MQTT site.
type MQTTSiteStatus struct {
	SiteID    string `json:"siteId"`
	Connected bool   `json:"connected"`
	BrokerURL string `json:"brokerUrl,omitempty"`
}

// ---- MQTT Sites ----

func (s *Server) handleListMQTTSites(w http.ResponseWriter, r *http.Request) {
	// Read MQTT site configs from AppConfig
	var sites []MQTTSiteConfig
	if err := s.svc.AppCfg.GetTyped("mqtt.sites", &sites); err != nil || sites == nil {
		sites = []MQTTSiteConfig{}
	}
	writeJSON(w, http.StatusOK, sites)
}

func (s *Server) handleUpdateMQTTSite(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")

	var site MQTTSiteConfig
	if err := readJSON(r, &site); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	site.SiteID = siteID

	// Load existing sites
	var sites []MQTTSiteConfig
	_ = s.svc.AppCfg.GetTyped("mqtt.sites", &sites)

	// Update or append
	found := false
	for i, existing := range sites {
		if existing.SiteID == siteID {
			sites[i] = site
			found = true
			break
		}
	}
	if !found {
		sites = append(sites, site)
	}

	if err := s.svc.AppCfg.Set(r.Context(), "mqtt.sites", sites); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update MQTT site")
		return
	}

	writeJSON(w, http.StatusOK, site)
}

// ---- MQTT Sites Status ----

func (s *Server) handleMQTTSitesStatus(w http.ResponseWriter, r *http.Request) {
	// If MQTT service is available, report its connection status
	connected := false
	if s.svc.MQTT != nil {
		connected = s.svc.MQTT.IsConnected()
	}

	var sites []MQTTSiteConfig
	_ = s.svc.AppCfg.GetTyped("mqtt.sites", &sites)

	statuses := make([]MQTTSiteStatus, 0, len(sites))
	for _, site := range sites {
		statuses = append(statuses, MQTTSiteStatus{
			SiteID:    site.SiteID,
			Connected: connected && site.Enabled,
			BrokerURL: site.BrokerURL,
		})
	}

	// If no sites configured, show the main connection
	if len(statuses) == 0 {
		statuses = append(statuses, MQTTSiteStatus{
			SiteID:    "local",
			Connected: connected,
			BrokerURL: s.cfg.MQTTBrokerURL,
		})
	}

	writeJSON(w, http.StatusOK, statuses)
}

// ---- MQTT Test / Restart ----

func (s *Server) handleTestMQTTSite(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")

	if s.svc.MQTT == nil {
		writeError(w, http.StatusServiceUnavailable, "MQTT service not enabled")
		return
	}

	// Test by checking if the main MQTT client is connected
	connected := s.svc.MQTT.IsConnected()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"siteId":    siteID,
		"connected": connected,
		"message":   "MQTT connection test complete",
	})
}

func (s *Server) handleRestartMQTTSite(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteId")

	if s.svc.MQTT == nil {
		writeError(w, http.StatusServiceUnavailable, "MQTT service not enabled")
		return
	}

	// Stop and restart the MQTT connection
	s.svc.MQTT.Stop()
	if err := s.svc.MQTT.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restart MQTT: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"siteId":  siteID,
		"status":  "restarted",
		"message": "MQTT connection restarted",
	})
}

// ---- MQTT Config ----

func (s *Server) handleGetMQTTConfig(w http.ResponseWriter, r *http.Request) {
	cfg := map[string]interface{}{
		"enabled":   s.cfg.MQTTEnabled,
		"brokerUrl": s.cfg.MQTTBrokerURL,
	}

	// Overlay dynamic config
	keys := []string{"mqtt.qos", "mqtt.keepAlive", "mqtt.cleanSession", "mqtt.topicPrefix"}
	for _, key := range keys {
		if val, ok := s.svc.AppCfg.Get(key); ok {
			cfg[key] = json.RawMessage(val)
		}
	}

	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateMQTTConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for key, value := range body {
		prefixedKey := "mqtt." + key
		if err := s.svc.AppCfg.Set(r.Context(), prefixedKey, value); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set config key: "+key)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
