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
	"github.com/karamble/diginode-cc/internal/alerts"
	"github.com/karamble/diginode-cc/internal/api"
	"github.com/karamble/diginode-cc/internal/audit"
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
	// Load configuration (before logger so we can use LogLevel)
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Structured logging with configurable level
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	slog.Info("starting DigiNode CC", "version", Version)

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
	hub := ws.NewHub(cfg.WSMaxClients)
	go hub.Run()

	// Serial port manager (Meshtastic)
	serialMgr := serial.NewManager(cfg, hub)

	// Instantiate all domain services
	authSvc := auth.NewService(db, cfg.JWTSecret, auth.AuthConfig{
		JWTExpiry:                cfg.JWTExpiry,
		LockoutThreshold:         cfg.AuthLockoutThreshold,
		LockoutDurationMinutes:   cfg.AuthLockoutDurationMinutes,
		PasswordResetExpiryHours: cfg.PasswordResetExpiryHours,
		TwoFactorIssuer:          cfg.TwoFactorIssuer,
	})
	usersSvc := users.NewService(db, cfg.InviteExpiryHours)
	sitesSvc := sites.NewService(db)
	nodesSvc := nodes.NewService(db, hub)
	dronesSvc := drones.NewService(db, hub)
	dronesSvc.SetNodeLookup(nodesSvc.LookupNodeIDAndSite)
	inventorySvc := inventory.NewService(db, hub)
	dronesSvc.SetInventoryCallback(inventorySvc.Track)
	faaSvc := faa.NewService(db, cfg.FAAOnlineLookupEnabled, cfg.FAACacheTTLMinutes)
	dronesSvc.SetFAALookup(func(ctx context.Context, droneID, mac, serial string) (map[string]interface{}, error) {
		entry, err := faaSvc.LookupMultiKey(ctx, droneID, mac, serial)
		if err != nil || entry == nil {
			return nil, err
		}
		data := map[string]interface{}{
			"serialNumber":    entry.SerialNumber,
			"registration":    entry.Registration,
			"nNumber":         entry.Registration,
			"manufacturer":    entry.Manufacturer,
			"makeName":        entry.Manufacturer,
			"model":           entry.Model,
			"modelName":       entry.Model,
			"registrantName":  entry.RegistrantName,
			"registrantCity":  entry.RegistrantCity,
			"registrantState": entry.RegistrantState,
			"fccIdentifier":   entry.FccIdentifier,
			"modeSCodeHex":    entry.ModeSCodeHex,
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
				ID:            g.ID,
				Name:          g.Name,
				AlarmLevel:    g.AlarmLevel,
				AlarmMessage:  g.AlarmMessage,
				NotifyWebhook: g.NotifyWebhook,
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
		Enabled:  cfg.MailEnabled,
		Secure:   cfg.MailSecure,
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
	})
	alertsSvc.SetEmailSender(mailSvc.Send)
	appCfg := config.NewAppConfig(db.Pool)

	// ADS-B service (optional)
	var adsbSvc *adsb.Service
	if cfg.ADSBEnabled {
		adsbSvc = adsb.NewService(hub, cfg.ADSBURL, cfg.ADSBPollIntervalMS)
	} else {
		adsbSvc = adsb.NewService(hub, "", cfg.ADSBPollIntervalMS)
	}
	if cfg.ADSBOpenSkyEnabled && cfg.ADSBOpenSkyClientID != "" {
		adsbSvc.OpenSky = adsb.NewOpenSkyClient(cfg.ADSBOpenSkyClientID, cfg.ADSBOpenSkyClientSecret)
		slog.Info("OpenSky enrichment enabled")
	}
	if cfg.ADSBPlanespottersEnabled {
		adsbSvc.Planespotters = adsb.NewPlanespottersClient()
		slog.Info("Planespotters enrichment enabled")
	}

	// MQTT service (optional)
	var mqttSvc *mqtt.Service
	if cfg.MQTTEnabled {
		mqttSvc = mqtt.NewService(hub, cfg.MQTTBrokerURL, "local", cfg.MQTTConnectTimeoutMS)
	}

	// Updates service
	updatesSvc := updates.NewService(".", cfg.AutoUpdateRemote, cfg.AutoUpdateBranch)

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
	dispatcher.SetSerialManager(serialMgr)
	serialMgr.RegisterHandler(dispatcher.HandlePacket)

	// AntiHunter heartbeats arrive as text over the mesh (no Position protobuf).
	// Promote the embedded GPS fix into a node position update so remote sensor
	// nodes show up on the map and their lastHeard/position refreshes without a
	// separate Meshtastic Position packet.
	serialMgr.SetMeshTelemetryCallback(func(from uint32, lat, lon float64, _ map[string]interface{}) {
		pos := &serial.PositionData{
			LatitudeI:  int32(lat * 1e7),
			LongitudeI: int32(lon * 1e7),
			Time:       uint32(time.Now().Unix()),
		}
		nodesSvc.HandlePosition(from, pos)
	})

	// Wire target-detected events → inventory + alerts + webhooks + geofences
	serialMgr.SetTargetDetectedCallback(func(mac, ssid, deviceType string, rssi, channel int, lat, lon float64, nodeID string) {
		// 1. Inventory upsert with OUI lookup
		manufacturer := inventory.LookupOUI(mac)
		inventorySvc.TrackFull(mac, manufacturer, ssid, deviceType, rssi, nodeID, lat, lon, channel)

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

	// Wire triangulation protocol (T_D / T_F / T_C) → target tracking service
	trackingSvc := targets.NewTrackingService(targetsSvc)
	serialMgr.SetTriangulationCallbacks(
		// T_D: intermediate detection data → feed tracking sliding window
		func(mac, nodeID string, rssi int, lat, lon float64) {
			targetsSvc.EnsureTargetExists(context.Background(), mac, nodeID)
			trackingSvc.AddDetection(context.Background(), mac, nodeID, lat, lon, rssi)
		},
		// T_F: final triangulation fix → apply position with confidence
		func(mac string, lat, lon, confidence, uncertainty float64) {
			if err := targetsSvc.ApplyTrackingEstimate(context.Background(), mac, lat, lon, confidence, uncertainty, "firmware-triangulation"); err != nil {
				slog.Debug("T_F apply failed", "mac", mac, "error", err)
			}
		},
		// T_C: triangulation complete → log
		func(mac string, nodes int) {
			slog.Info("triangulation complete", "mac", mac, "nodes", nodes)
		},
	)
	_ = trackingSvc // used via callbacks

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
	// Load IEEE OUI database (try data/oui.csv, non-fatal if missing)
	if n, err := inventory.LoadOUIFromFile("data/oui.csv"); err != nil {
		slog.Warn("OUI database not loaded (run GET /api/oui/import to download)", "error", err)
	} else {
		slog.Info("OUI database loaded", "entries", n)
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
		Auth:        authSvc,
		Users:       usersSvc,
		Sites:       sitesSvc,
		Nodes:       nodesSvc,
		Drones:      dronesSvc,
		Chat:        chatSvc,
		Commands:    commandsSvc,
		Alerts:      alertsSvc,
		Geofences:   geofencesSvc,
		Targets:     targetsSvc,
		Inventory:   inventorySvc,
		Webhooks:    webhooksSvc,
		Alarms:      alarmsSvc,
		Firewall:    firewallSvc,
		FAA:         faaSvc,
		Exports:     exportsSvc,
		Mail:        mailSvc,
		AppCfg:      appCfg,
		Permissions: permsSvc,
		Audit:       auditSvc,
		ADSB:        adsbSvc,
		MQTT:        mqttSvc,
		Updates:     updatesSvc,
		Database:    db,
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
