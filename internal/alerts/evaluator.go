package alerts

import (
	"context"
	"fmt"
	"strings"
)

// DetectionEvent represents data from a detection to evaluate against rules.
type DetectionEvent struct {
	MAC      string
	OUI      string // first 8 chars of MAC (XX:XX:XX)
	SSID     string
	Channel  int
	RSSI     int
	NodeID   string
	NodeName string
}

// Evaluate checks a detection event against all active rules and fires matching alerts.
func (s *Service) Evaluate(ctx context.Context, evt DetectionEvent) {
	s.mu.RLock()
	rules := make([]*Rule, 0, len(s.rules))
	for _, r := range s.rules {
		if r.Enabled {
			rules = append(rules, r)
		}
	}
	s.mu.RUnlock()

	for _, rule := range rules {
		if s.matchesRule(rule, evt) {
			msg := s.renderTemplate(rule, evt)
			title := rule.Name
			data := map[string]interface{}{
				"mac":      evt.MAC,
				"oui":      evt.OUI,
				"ssid":     evt.SSID,
				"channel":  evt.Channel,
				"rssi":     evt.RSSI,
				"nodeId":   evt.NodeID,
				"nodeName": evt.NodeName,
			}
			s.Trigger(ctx, rule.ID, title, msg, data)
		}
	}
}

func (s *Service) matchesRule(rule *Rule, evt DetectionEvent) bool {
	cond := rule.Condition
	if cond == nil {
		return false
	}

	matchMode, _ := cond["matchMode"].(string) // "ANY" or "ALL"
	if matchMode == "" {
		matchMode = "ANY"
	}

	var checks []bool

	// MAC address match
	if macs, ok := cond["macAddresses"].([]interface{}); ok && len(macs) > 0 {
		matched := false
		for _, m := range macs {
			if ms, ok := m.(string); ok && strings.EqualFold(ms, evt.MAC) {
				matched = true
				break
			}
		}
		checks = append(checks, matched)
	}

	// OUI prefix match
	if ouis, ok := cond["ouiPrefixes"].([]interface{}); ok && len(ouis) > 0 {
		matched := false
		for _, o := range ouis {
			if os, ok := o.(string); ok && strings.HasPrefix(strings.ToUpper(evt.MAC), strings.ToUpper(os)) {
				matched = true
				break
			}
		}
		checks = append(checks, matched)
	}

	// SSID match
	if ssids, ok := cond["ssids"].([]interface{}); ok && len(ssids) > 0 {
		matched := false
		for _, ss := range ssids {
			if sv, ok := ss.(string); ok && strings.EqualFold(sv, evt.SSID) {
				matched = true
				break
			}
		}
		checks = append(checks, matched)
	}

	// Channel match
	if channels, ok := cond["channels"].([]interface{}); ok && len(channels) > 0 {
		matched := false
		for _, ch := range channels {
			if cf, ok := ch.(float64); ok && int(cf) == evt.Channel {
				matched = true
				break
			}
		}
		checks = append(checks, matched)
	}

	// RSSI range
	if minR, ok := cond["minRssi"].(float64); ok && evt.RSSI != 0 {
		checks = append(checks, evt.RSSI >= int(minR))
	}
	if maxR, ok := cond["maxRssi"].(float64); ok && evt.RSSI != 0 {
		checks = append(checks, evt.RSSI <= int(maxR))
	}

	if len(checks) == 0 {
		return false
	}

	if matchMode == "ALL" {
		for _, c := range checks {
			if !c {
				return false
			}
		}
		return true
	}
	// ANY mode
	for _, c := range checks {
		if c {
			return true
		}
	}
	return false
}

func (s *Service) renderTemplate(rule *Rule, evt DetectionEvent) string {
	tmpl, _ := rule.Condition["messageTemplate"].(string)
	if tmpl == "" {
		return fmt.Sprintf("Alert rule '%s' triggered", rule.Name)
	}
	r := strings.NewReplacer(
		"{mac}", evt.MAC,
		"{oui}", evt.OUI,
		"{ssid}", evt.SSID,
		"{channel}", fmt.Sprintf("%d", evt.Channel),
		"{rssi}", fmt.Sprintf("%d", evt.RSSI),
		"{nodeId}", evt.NodeID,
		"{nodeName}", evt.NodeName,
		"{rule}", rule.Name,
		"{severity}", string(rule.Severity),
	)
	return r.Replace(tmpl)
}
