package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/firewall"
)

// handleFirewallStatus returns the overall firewall status.
func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) {
	jailed, err := s.svc.Firewall.GetJailedIPs(r.Context())
	if err != nil {
		jailed = nil // non-fatal, report 0
	}
	jailedCount := 0
	if jailed != nil {
		jailedCount = len(jailed)
	}

	// Read firewall config from app_config
	var fwCfg struct {
		Enabled       bool   `json:"enabled"`
		DefaultPolicy string `json:"defaultPolicy"`
	}
	fwCfg.Enabled = true
	fwCfg.DefaultPolicy = "allow"
	_ = s.svc.AppCfg.GetTyped("firewall", &fwCfg)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":       fwCfg.Enabled,
		"defaultPolicy": fwCfg.DefaultPolicy,
		"ruleCount":     s.svc.Firewall.RuleCount(),
		"jailedCount":   jailedCount,
	})
}

// handleUpdateFirewallConfig updates the firewall configuration.
func (s *Server) handleUpdateFirewallConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled       *bool   `json:"enabled"`
		DefaultPolicy *string `json:"defaultPolicy"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Read existing config
	var fwCfg struct {
		Enabled       bool   `json:"enabled"`
		DefaultPolicy string `json:"defaultPolicy"`
	}
	fwCfg.Enabled = true
	fwCfg.DefaultPolicy = "allow"
	_ = s.svc.AppCfg.GetTyped("firewall", &fwCfg)

	if body.Enabled != nil {
		fwCfg.Enabled = *body.Enabled
	}
	if body.DefaultPolicy != nil {
		fwCfg.DefaultPolicy = *body.DefaultPolicy
	}

	if err := s.svc.AppCfg.Set(r.Context(), "firewall", fwCfg); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update firewall config")
		return
	}

	writeJSON(w, http.StatusOK, fwCfg)
}

// handleListJailedIPs returns all temporarily blocked (jailed) IPs.
func (s *Server) handleListJailedIPs(w http.ResponseWriter, r *http.Request) {
	jailed, err := s.svc.Firewall.GetJailedIPs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list jailed IPs")
		return
	}
	if jailed == nil {
		jailed = []*firewall.JailedIP{}
	}
	writeJSON(w, http.StatusOK, jailed)
}

// handleUnjailIP removes a temporary block (unjail) by ID.
func (s *Server) handleUnjailIP(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Firewall.UnjailIP(r.Context(), id); err != nil {
		if firewall.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "jailed IP not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to unjail IP")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleFirewallLogs returns recent firewall-related audit log entries.
func (s *Server) handleFirewallLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := s.svc.Firewall.GetFirewallLogs(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve firewall logs")
		return
	}
	if entries == nil {
		entries = []*firewall.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
