package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/karamble/diginode-cc/internal/adsb"
	"github.com/karamble/diginode-cc/internal/alarms"
	"github.com/karamble/diginode-cc/internal/audit"
	"github.com/karamble/diginode-cc/internal/alerts"
	"github.com/karamble/diginode-cc/internal/api"
	"github.com/karamble/diginode-cc/internal/auth"
	"github.com/karamble/diginode-cc/internal/chat"
	"github.com/karamble/diginode-cc/internal/commands"
	"github.com/karamble/diginode-cc/internal/config"
	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/drones"
	"github.com/karamble/diginode-cc/internal/exports"
	"github.com/karamble/diginode-cc/internal/faa"
	"github.com/karamble/diginode-cc/internal/firewall"
	"github.com/karamble/diginode-cc/internal/geofences"
	"github.com/karamble/diginode-cc/internal/inventory"
	"github.com/karamble/diginode-cc/internal/mail"
	"github.com/karamble/diginode-cc/internal/meshtastic"
	"github.com/karamble/diginode-cc/internal/mqtt"
	"github.com/karamble/diginode-cc/internal/nodes"
	"github.com/karamble/diginode-cc/internal/permissions"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/sites"
	"github.com/karamble/diginode-cc/internal/targets"
	"github.com/karamble/diginode-cc/internal/updates"
	"github.com/karamble/diginode-cc/internal/users"
	"github.com/karamble/diginode-cc/internal/webhooks"
	"github.com/karamble/diginode-cc/internal/ws"
)

func main() {
	// Structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting DigiNode CC", "version", Version)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Connect to PostgreSQL
	db, err := database.New(cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// WebSocket hub
	hub := ws.NewHub()
	go hub.Run()

	// Serial port manager (Meshtastic)
	serialMgr := serial.NewManager(cfg, hub)

	// Instantiate all domain services
	authSvc := auth.NewService(db, cfg.JWTSecret)
	usersSvc := users.NewService(db)
	sitesSvc := sites.NewService(db)
	nodesSvc := nodes.NewService(db, hub)
	dronesSvc := drones.NewService(db, hub)
	dronesSvc.SetNodeLookup(nodesSvc.LookupNodeIDAndSite)
	inventorySvc := inventory.NewService(db, hub)
	dronesSvc.SetInventoryCallback(inventorySvc.Track)
	faaSvc := faa.NewService(db)
	dronesSvc.SetFAALookup(func(ctx context.Context, serial string) (map[string]interface{}, error) {
		entry, err := faaSvc.Lookup(ctx, serial)
		if err != nil {
			return nil, err
		}
		data := map[string]interface{}{
			"serialNumber":    entry.SerialNumber,
			"registration":    entry.Registration,
			"manufacturer":    entry.Manufacturer,
			"model":           entry.Model,
			"registrantName":  entry.RegistrantName,
			"registrantCity":  entry.RegistrantCity,
			"registrantState": entry.RegistrantState,
		}
		return data, nil
	})
	chatSvc := chat.NewService(db, hub)
	chatSvc.SetBufferCallback(serialMgr.AddTextMessage)
	commandsSvc := commands.NewService(db, hub)
	alertsSvc := alerts.NewService(db, hub)
	webhooksSvc := webhooks.NewService(db)
	geofencesSvc := geofences.NewService(db, hub)
	dronesSvc.SetGeofenceChecker(func(lat, lon float64, entityType string) []drones.GeofenceHit {
		triggered := geofencesSvc.CheckPoint(lat, lon, entityType)
		hits := make([]drones.GeofenceHit, len(triggered))
		for i, g := range triggered {
			hits[i] = drones.GeofenceHit{
				ID:             g.ID,
				Name:           g.Name,
				AlarmLevel:     g.AlarmLevel,
				AlarmMessage:   g.AlarmMessage,
				NotifyWebhook:  g.NotifyWebhook,
			}
		}
		return hits
	})
	dronesSvc.SetGeofenceNotifier(func(geofenceID, geofenceName, entityType, entityID string, lat, lon float64, alarmLevel, message string, notifyWebhook bool) {
		// Broadcast geofence.event via WebSocket
		if g := geofencesSvc.GetByID(geofenceID); g != nil {
			geofencesSvc.NotifyViolation(g, entityType, entityID, lat, lon)
		}
		// Persist as alert event (shows in Recent Events on alerts page)
		severity := alerts.SeverityAlert
		switch alarmLevel {
		case "INFO":
			severity = alerts.SeverityInfo
		case "NOTICE":
			severity = alerts.SeverityNotice
		case "CRITICAL":
			severity = alerts.SeverityCritical
		}
		data := map[string]interface{}{
			"geofenceId":   geofenceID,
			"geofenceName": geofenceName,
			"entityType":   entityType,
			"entityId":     entityID,
			"latitude":     lat,
			"longitude":    lon,
		}
		title := fmt.Sprintf("Geofence breach: %s", geofenceName)
		alertsSvc.TriggerDirect(context.Background(), severity, title, message, data)

		// Fire webhook if enabled on this geofence
		if notifyWebhook {
			webhooksSvc.Dispatch("alert.geofence", map[string]interface{}{
				"geofenceId":   geofenceID,
				"geofenceName": geofenceName,
				"entityType":   entityType,
				"entityId":     entityID,
				"latitude":     lat,
				"longitude":    lon,
				"alarmLevel":   alarmLevel,
				"message":      message,
			})
		}
	})
	targetsSvc := targets.NewService(db, hub)
	alarmsSvc := alarms.NewService(db)
	firewallSvc := firewall.NewService(db)
	exportsSvc := exports.NewService(db)
	permsSvc := permissions.NewService(db)
	mailSvc := mail.NewService(mail.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
	})
	appCfg := config.NewAppConfig(db.Pool)

	// ADS-B service (optional)
	var adsbSvc *adsb.Service
	if cfg.ADSBEnabled {
		adsbSvc = adsb.NewService(hub, cfg.ADSBURL)
	} else {
		// Create a service with empty URL so handlers can still return empty data
		adsbSvc = adsb.NewService(hub, "")
	}

	// MQTT service (optional)
	var mqttSvc *mqtt.Service
	if cfg.MQTTEnabled {
		mqttSvc = mqtt.NewService(hub, cfg.MQTTBrokerURL, "local")
	}

	// Updates service
	updatesSvc := updates.NewService(".")

	// Wire Meshtastic dispatcher → domain services
	dispatcher := meshtastic.NewDispatcher(hub)
	dispatcher.SetNodeHandler(nodesSvc)
	dispatcher.SetDroneHandler(dronesSvc)
	dispatcher.SetChatHandler(chatSvc)
	dispatcher.SetDeviceTimeCallback(serialMgr.SetDeviceTime)
	dispatcher.SetAlertCallback(func(ctx context.Context, evt alerts.DetectionEvent) {
		alertsSvc.Evaluate(ctx, evt)
	})
	dispatcher.SetWebhookCallback(webhooksSvc.Dispatch)
	serialMgr.RegisterHandler(dispatcher.HandlePacket)

	// Wire target-detected events → inventory + alerts + webhooks + geofences
	serialMgr.SetTargetDetectedCallback(func(mac, ssid, deviceType string, rssi, channel int, lat, lon float64, nodeID string) {
		// 1. Inventory upsert with OUI lookup
		manufacturer := inventory.LookupOUI(mac)
		inventorySvc.TrackFull(mac, manufacturer, ssid, deviceType, rssi, nodeID, lat, lon)

		// 2. Alert rule evaluation
		oui := ""
		if len(mac) >= 8 {
			oui = mac[:8]
		}
		alertsSvc.Evaluate(context.Background(), alerts.DetectionEvent{
			MAC:     mac,
			OUI:     oui,
			SSID:    ssid,
			Channel: channel,
			RSSI:    rssi,
			NodeID:  nodeID,
		})

		// 3. Webhook dispatch
		webhooksSvc.Dispatch("target.detected", map[string]interface{}{
			"mac":        mac,
			"ssid":       ssid,
			"deviceType": deviceType,
			"rssi":       rssi,
			"channel":    channel,
			"latitude":   lat,
			"longitude":  lon,
			"nodeId":     nodeID,
		})

		// 4. Geofence check (if target has GPS)
		if lat != 0 && lon != 0 {
			triggered := geofencesSvc.CheckPoint(lat, lon, "target")
			for _, g := range triggered {
				geofencesSvc.NotifyViolation(g, "target", mac, lat, lon)
			}
		}
	})

	// Load startup data from DB
	ctx := context.Background()
	if err := alertsSvc.Load(ctx); err != nil {
		slog.Warn("failed to load alert rules", "error", err)
	}
	if err := geofencesSvc.Load(ctx); err != nil {
		slog.Warn("failed to load geofences", "error", err)
	}
	if err := webhooksSvc.Load(ctx); err != nil {
		slog.Warn("failed to load webhooks", "error", err)
	}
	if err := alarmsSvc.Load(ctx); err != nil {
		slog.Warn("failed to load alarms", "error", err)
	}
	if err := firewallSvc.Load(ctx); err != nil {
		slog.Warn("failed to load firewall rules", "error", err)
	}
	if err := inventorySvc.Load(ctx); err != nil {
		slog.Warn("failed to load inventory", "error", err)
	}
	if err := targetsSvc.Load(ctx); err != nil {
		slog.Warn("failed to load targets", "error", err)
	}
	if err := appCfg.Load(ctx); err != nil {
		slog.Warn("failed to load app config", "error", err)
	}
	if err := appCfg.EnsureDefaults(ctx); err != nil {
		slog.Warn("failed to ensure app config defaults", "error", err)
	}

	// Audit logging service
	auditSvc := audit.NewService(db)

	// Start serial manager (always runs; retries until device appears)
	go func() {
		if err := serialMgr.Start(); err != nil {
			slog.Error("serial manager failed", "error", err)
		}
	}()

	// Start ADS-B poller
	if cfg.ADSBEnabled {
		go adsbSvc.Start(ctx)
	}

	// Start MQTT service
	if mqttSvc != nil {
		if err := mqttSvc.Start(); err != nil {
			slog.Warn("failed to start MQTT service", "error", err)
		}
	}

	// Bundle services for the API server
	svc := &api.Services{
		Auth:      authSvc,
		Users:     usersSvc,
		Sites:     sitesSvc,
		Nodes:     nodesSvc,
		Drones:    dronesSvc,
		Chat:      chatSvc,
		Commands:  commandsSvc,
		Alerts:    alertsSvc,
		Geofences: geofencesSvc,
		Targets:   targetsSvc,
		Inventory: inventorySvc,
		Webhooks:  webhooksSvc,
		Alarms:    alarmsSvc,
		Firewall:  firewallSvc,
		FAA:       faaSvc,
		Exports:   exportsSvc,
		Mail:      mailSvc,
		AppCfg:      appCfg,
		Permissions: permsSvc,
		Audit:       auditSvc,
		ADSB:        adsbSvc,
		MQTT:        mqttSvc,
		Updates:     updatesSvc,
	}

	// HTTP server
	srv := api.NewServer(cfg, hub, serialMgr, svc)
	httpServer := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      srv.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("HTTP server listening", "addr", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Start daily pruning goroutine
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pruneCtx := context.Background()
				if n, err := nodesSvc.PrunePositions(pruneCtx, 30); err != nil {
					slog.Warn("failed to prune node positions", "error", err)
				} else if n > 0 {
					slog.Info("pruned old node positions", "deleted", n)
				}
				if n, err := dronesSvc.PruneDetections(pruneCtx, 30); err != nil {
					slog.Warn("failed to prune drone detections", "error", err)
				} else if n > 0 {
					slog.Info("pruned old drone detections", "deleted", n)
				}
				if n, err := commandsSvc.PruneOldCommands(pruneCtx, 180); err != nil {
					slog.Warn("failed to prune old commands", "error", err)
				} else if n > 0 {
					slog.Info("pruned old commands", "deleted", n)
				}
			case <-sigCtx.Done():
				return
			}
		}
	}()

	<-sigCtx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serialMgr.Stop()
	if cfg.ADSBEnabled {
		adsbSvc.Stop()
	}
	if mqttSvc != nil {
		mqttSvc.Stop()
	}
	hub.Stop()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	fmt.Println("DigiNode CC stopped.")
}
