package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/karamble/diginode-cc/internal/adsb"
	"github.com/karamble/diginode-cc/internal/alarms"
	"github.com/karamble/diginode-cc/internal/alerts"
	"github.com/karamble/diginode-cc/internal/api"
	"github.com/karamble/diginode-cc/internal/audit"
	"github.com/karamble/diginode-cc/internal/auth"
	"github.com/karamble/diginode-cc/internal/bleclassify"
	"github.com/karamble/diginode-cc/internal/chat"
	"github.com/karamble/diginode-cc/internal/commands"
	"github.com/karamble/diginode-cc/internal/config"
	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/drones"
	"github.com/karamble/diginode-cc/internal/exports"
	"github.com/karamble/diginode-cc/internal/faa"
	"github.com/karamble/diginode-cc/internal/firewall"
	"github.com/karamble/diginode-cc/internal/fleetsec"
	fleetsecjobs "github.com/karamble/diginode-cc/internal/fleetsec/jobs"
	"github.com/karamble/diginode-cc/internal/geofences"
	"github.com/karamble/diginode-cc/internal/inventory"
	"github.com/karamble/diginode-cc/internal/mail"
	"github.com/karamble/diginode-cc/internal/meshtastic"
	"github.com/karamble/diginode-cc/internal/mqtt"
	"github.com/karamble/diginode-cc/internal/nodes"
	"github.com/karamble/diginode-cc/internal/permissions"
	"github.com/karamble/diginode-cc/internal/probes"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/sites"
	"github.com/karamble/diginode-cc/internal/statusbroadcast"
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
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := nodesSvc.Load(loadCtx); err != nil {
		slog.Warn("failed to load persisted nodes", "error", err)
	}
	loadCancel()
	dronesSvc := drones.NewService(db, hub)
	dronesSvc.SetNodeLookup(nodesSvc.LookupNodeIDAndSite)
	inventorySvc := inventory.NewService(db, hub)
	dronesSvc.SetInventoryCallback(inventorySvc.Track)
	probesSvc := probes.NewService(db)
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
	// Every AntiHunter command rides a Meshtastic TEXTMSG whose body is the
	// on-wire line built by commands.Build (e.g. "@ALL SCAN_START:2:60:1,6,11").
	// The AntiHunter dispatcher inside each remote Heltec parses the @TARGET
	// prefix itself, so we always broadcast at the mesh layer and let the
	// firmware filter by node-id — no per-target Meshtastic routing needed.
	// Mirror outbound command lines into chat_messages so the operator
	// sees them in the chat tab alongside other broadcast traffic. The
	// commands worker calls this only on a successful TX.
	commandsSvc.SetChatEcho(chatSvc.PersistAndBroadcast)
	commandsSvc.SetSendFunc(func(_ uint32, _ string, payload []byte) error {
		return serialMgr.SendToRadio(
			serial.BuildTextMessage(serial.BroadcastAddr, string(payload)),
		)
	})
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

	// AntiHunter heartbeats arrive as text over the mesh (no Position or
	// Telemetry protobuf). Promote the embedded GPS fix and temperature reading
	// into a node update so remote sensor nodes show up on the map and the
	// expanded node-list row shows temp + last line without polling alerts.
	serialMgr.SetMeshTelemetryCallback(nodesSvc.HandleAntihunterHeartbeat)

	// AntiHunter reply frames (*_ACK lines) close out pending command rows.
	// The serial manager parses them into command-ack events and hands them
	// to the commands service, which matches by ACK type and flips the
	// lifecycle to OK / ERROR.
	serialMgr.SetCommandAckCallback(commandsSvc.HandleStructuredACK)

	// Route every Kind="alert" event (tamper, vibration, setup-mode,
	// identity, erase, battery-saver state, scan/baseline/drone/deauth
	// *_DONE summaries) through the alerts service. TriggerDirect persists
	// to alert_events and broadcasts on the WS hub so the terminal page,
	// dashboard feed, and email/webhook pipelines all see them.
	serialMgr.SetAlertCallback(func(category, level, nodeID, raw string, data map[string]interface{}) {
		sev := alerts.SeverityInfo
		switch strings.ToUpper(level) {
		case "CRITICAL":
			sev = alerts.SeverityCritical
		case "ALERT":
			sev = alerts.SeverityAlert
		case "NOTICE":
			sev = alerts.SeverityNotice
		}
		title := category
		if nodeID != "" {
			title = nodeID + ": " + category
		}
		enriched := make(map[string]interface{}, len(data)+2)
		for k, v := range data {
			enriched[k] = v
		}
		if nodeID != "" {
			enriched["nodeId"] = nodeID
		}
		enriched["category"] = category
		alertsSvc.TriggerDirect(context.Background(), sev, title, raw, enriched)

		// Probe-request scanner hits also feed the SSID-keyed history table,
		// which is the actually-useful pivot since modern devices randomize
		// MAC on every probe — the SSID is the stable identity that reveals
		// location overlap across the sensor mesh.
		if category == "probe" {
			// Skip duplicates: the same physical PROBE_HIT lands here twice
			// — once via ProcessMeshText (which overrides NodeID to the
			// Meshtastic hex form like "!02ed5f04") and once via the local
			// UART debug echo (which keeps the firmware-emitted "AH64"
			// prefix). Without dedup probe_ssids gets two rows per real
			// hit and probeHits double-counts. Keying on the raw line is
			// reliable since both paths receive byte-identical text.
			if isDuplicateAlert(raw) {
				return
			}

			ssid, _ := data["ssid"].(string)
			mac, _ := data["mac"].(string)
			rssi, _ := data["rssi"].(int)
			channel, _ := data["channel"].(int)
			ghost, _ := data["ghostSsid"].(bool)
			dst, _ := data["dstMatch"].(bool)

			// Canonicalize the source-node identifier to the AH short ID
			// (e.g. "AH64") so probe_ssids primary-keys merge across the
			// dual-path arrivals — even though the dedup above should
			// already prevent the second arrival from reaching here, this
			// guarantees consistency for any future single-path event too.
			canonicalNode := canonicalProbeNode(nodeID, nodesSvc)

			probesSvc.Track(ssid, canonicalNode, mac, rssi, channel, ghost, dst)
			// Bump the live probeHits counter on the running PROBE_START
			// command so the operator sees progress in the details modal.
			commandsSvc.RecordProbeHit(canonicalNode)
		}
	})

	// Wire target-detected events → inventory + alerts + webhooks + geofences
	serialMgr.SetTargetDetectedCallback(func(mac, ssid, deviceType string, rssi, channel int, lat, lon float64, nodeID, targetID string) {
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
		webhookPayload := map[string]interface{}{
			"mac":        mac,
			"ssid":       ssid,
			"deviceType": deviceType,
			"rssi":       rssi,
			"channel":    channel,
			"latitude":   lat,
			"longitude":  lon,
			"nodeId":     nodeID,
		}
		// BLE fingerprint hit — decorate with the matched target's name
		// + short ID so webhook subscribers don't need to round-trip the
		// targets API to get a human-readable label.
		if strings.HasPrefix(targetID, "T-B-") {
			if t := targetsSvc.FindByBLEShortID(targetID); t != nil {
				webhookPayload["bleTargetShortId"] = t.BLEShortID
				webhookPayload["bleTargetName"] = t.Name
				webhookPayload["bleTargetId"] = t.ID
			}
		}
		webhooksSvc.Dispatch("target.detected", webhookPayload)

		// 4. Geofence check (if target has GPS)
		if lat != 0 && lon != 0 {
			triggered := geofencesSvc.CheckPoint(lat, lon, "target")
			for _, g := range triggered {
				geofencesSvc.NotifyViolation(g, "target", mac, lat, lon)
			}
		}

		// 5. Scan-row correlation: append a [DEVICE] row to any RUNNING
		//    DEVICE_SCAN_START / SCAN_START targeting this node so the
		//    CommandsPage modal can render the per-device list when the
		//    scan terminates.
		commandsSvc.RecordScanDetection(nodeID, commands.ScanDetection{
			MAC:          mac,
			RSSI:         rssi,
			Band:         deviceType,
			Source:       "DEVICE",
			Manufacturer: manufacturer,
			LocalName:    ssid,
		})

		// 6. Per-hit history: record into target_hits when this observation
		//    resolves to a known target row. BLE fingerprint hits resolve
		//    via TID:T-B-####; legacy WiFi MAC targets resolve via the
		//    observed MAC. Untargeted observations are intentionally
		//    skipped — they live in inventory_devices.
		var matched *targets.Target
		if strings.HasPrefix(targetID, "T-B-") {
			matched = targetsSvc.FindByBLEShortID(targetID)
		} else {
			matched = targetsSvc.FindByMAC(strings.ToUpper(mac))
		}
		if matched != nil {
			rs := int16(rssi)
			h := targets.Hit{
				TargetID:      matched.ID,
				TargetShortID: matched.BLEShortID,
				ObservedMAC:   strings.ToUpper(mac),
				ObservedName:  ssid,
				RSSI:          &rs,
				NodeID:        nodeID,
			}
			if lat != 0 && lon != 0 {
				lt := lat
				ln := lon
				h.Latitude = &lt
				h.Longitude = &ln
			}
			if err := targetsSvc.RecordHit(context.Background(), &h); err != nil {
				slog.Warn("failed to record target hit", "target_id", matched.ID, "error", err)
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

	// BLE classification: every BLERAW: wire frame from Halberd sensors is
	// forwarded to the localhost lookupper. The Classify call has a
	// per-call timeout and persists the row with null classification fields
	// on any failure, so a lookupper that's slow, restarting, or not yet
	// up at boot does not block the serial dispatch path and self-heals on
	// its next reachable call.
	lookupper := bleclassify.NewLookupper()
	bleSvc := bleclassify.NewService(db, hub, lookupper)
	// Mirror BLE classifications into RUNNING scans as [BLERAW] rows so
	// the CommandsPage modal differentiates raw-classified hits from
	// streaming D: detections in the per-device summary it renders on
	// SCAN_DONE_ACK / STOP_ACK.
	bleSvc.SetClassifiedCallback(func(nodeID, mac string, rssi int, result *bleclassify.ClassifyResult) {
		det := commands.ScanDetection{
			MAC:    mac,
			RSSI:   rssi,
			Source: "BLERAW",
		}
		if result != nil {
			det.DetectionType = result.DetectionType
			det.Manufacturer = result.Manufacturer
			det.LocalName = result.LocalName
		}
		commandsSvc.RecordScanDetection(nodeID, det)
	})
	serialMgr.SetBLERawCallback(bleSvc.HandleRaw)
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

	// Fleet Security service: control-center identity, per-node trust
	// roster, channel PSK rotation. Wires its transaction tracker into
	// the dispatcher so inbound ADMIN/ROUTING acks land back here for
	// in-flight transaction resolution.
	fleetSecSvc := fleetsec.NewService(db, auditSvc, serialMgr, dispatcher)
	dispatcher.SetAdminReplyHandler(fleetSecSvc.Tracker())
	fleetSecSvc.WireHub(hub) // live PSK-rotation progress events

	// Durable jobs queue for fleet-security work. The polling worker
	// drives PSK rotation phases A/B/C out of band so HTTP handlers
	// return immediately and a container restart mid-rotation can
	// resume from the persisted job state.
	fleetJobsStore := fleetsecjobs.NewStore(db)
	fleetJobsLoop := fleetsecjobs.NewLoop(fleetJobsStore, "diginode-cc", slog.Default())
	fleetSecSvc.SetJobsStore(fleetJobsStore)
	fleetSecSvc.RegisterJobHandlers(fleetJobsLoop)

	// Stranded-node recovery: dispatcher hook + initial cache load.
	// The hook fires the recover_stranded job the instant a stranded
	// fleet member shows up on a recovery-cache channel hash. Map
	// is rebuilt after every Phase C completion automatically.
	recoveryHook := fleetSecSvc.SetupRecoveryHook()
	dispatcher.SetStrandedRecoveryHook(recoveryHook)
	go func() {
		// Initial cache load runs in background so a slow DB doesn't
		// block startup. Hook silently no-ops if its table is empty.
		if err := recoveryHook.RebuildHashTable(ctx); err != nil {
			slog.Warn("recovery hook: initial table build", "error", err)
		}
	}()

	if err := fleetJobsLoop.Start(ctx); err != nil {
		slog.Error("failed to start fleet-security jobs loop", "error", err)
	}
	defer fleetJobsLoop.Stop()

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

	// Mesh STATUS heartbeat broadcaster. Reads its enable + interval from
	// AppConfig on each tick so UI edits take effect without a restart.
	statusSvc := statusbroadcast.NewService(appCfg, nodesSvc, serialMgr, dispatcher)
	go statusSvc.Start(ctx)
	// On-demand STATUS reply: when an operator broadcasts "@NODE_<us> STATUS"
	// (or "@ALL STATUS") on the mesh, we answer with the same frame the
	// periodic broadcaster emits. Gated by statusReplyEnabled.
	dispatcher.SetStatusRequestHandler(statusSvc)

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
		Probes:      probesSvc,
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
		StatusBroadcast: statusSvc,
		BLEClassify: bleSvc,
		FleetSec:    fleetSecSvc,
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

	// Probe-scan watchdog: closes RUNNING PROBE_START rows whose explicit
	// duration window has elapsed, since the firmware emits no mesh signal
	// on natural duration end (only PROBE_ACK:STOPPED in response to
	// PROBE_STOP). Skips FOREVER scans intentionally.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				commandsSvc.EnforceScanTimeouts(5 * time.Second)
			case <-sigCtx.Done():
				return
			}
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
				if n, err := probesSvc.PruneMacSamples(pruneCtx, 24*time.Hour); err != nil {
					slog.Warn("failed to prune probe mac samples", "error", err)
				} else if n > 0 {
					slog.Info("pruned old probe mac samples", "deleted", n)
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

// alertDedupCache holds recent raw alert lines + their first-seen timestamp
// so the probe-event handler can drop the second arrival of a dual-routed
// PROBE_HIT (LoRa-routed mesh text + local UART debug echo). Sized small
// and pruned in-place — no goroutine needed.
var (
	alertDedupCache   = make(map[string]time.Time, 256)
	alertDedupMu      sync.Mutex
	alertDedupWindow  = 5 * time.Second
	alertDedupMaxKeys = 256
)

func isDuplicateAlert(raw string) bool {
	if raw == "" {
		return false
	}
	alertDedupMu.Lock()
	defer alertDedupMu.Unlock()
	now := time.Now()
	if last, ok := alertDedupCache[raw]; ok && now.Sub(last) < alertDedupWindow {
		// refresh the timestamp so a stream of identical retries keeps
		// extending the dedup window
		alertDedupCache[raw] = now
		return true
	}
	alertDedupCache[raw] = now
	if len(alertDedupCache) > alertDedupMaxKeys {
		cutoff := now.Add(-alertDedupWindow * 2)
		for k, t := range alertDedupCache {
			if t.Before(cutoff) {
				delete(alertDedupCache, k)
			}
		}
	}
	return false
}

// canonicalProbeNode normalizes a NodeID to the AH short ID ("AH64") form
// when possible, falling back to the input on parse failure or unknown
// node. Probe events arrive with two NodeID flavors: "!02ed5f04" (mesh hex,
// from ProcessMeshText) and "AH64" (firmware prefix, from local UART). The
// short ID is the operator-facing identity and what `@AH64` commands target.
func canonicalProbeNode(nodeID string, nodesSvc *nodes.Service) string {
	if nodeID == "" || nodesSvc == nil {
		return nodeID
	}
	if !strings.HasPrefix(nodeID, "!") {
		return nodeID
	}
	num, err := strconv.ParseUint(nodeID[1:], 16, 32)
	if err != nil {
		return nodeID
	}
	node := nodesSvc.GetByNodeNum(uint32(num))
	if node == nil || node.SensorShortID == "" {
		return nodeID
	}
	return node.SensorShortID
}
