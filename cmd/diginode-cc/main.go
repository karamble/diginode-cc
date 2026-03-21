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

	"github.com/karamble/diginode-cc/internal/alarms"
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
	"github.com/karamble/diginode-cc/internal/nodes"
	"github.com/karamble/diginode-cc/internal/permissions"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/sites"
	"github.com/karamble/diginode-cc/internal/targets"
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
	chatSvc := chat.NewService(db, hub)
	chatSvc.SetBufferCallback(serialMgr.AddTextMessage)
	commandsSvc := commands.NewService(db, hub)
	alertsSvc := alerts.NewService(db, hub)
	geofencesSvc := geofences.NewService(db, hub)
	targetsSvc := targets.NewService(db, hub)
	inventorySvc := inventory.NewService(db, hub)
	webhooksSvc := webhooks.NewService(db)
	alarmsSvc := alarms.NewService(db)
	firewallSvc := firewall.NewService(db)
	faaSvc := faa.NewService(db)
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

	// Wire Meshtastic dispatcher → domain services
	dispatcher := meshtastic.NewDispatcher(hub)
	dispatcher.SetNodeHandler(nodesSvc)
	dispatcher.SetDroneHandler(dronesSvc)
	dispatcher.SetChatHandler(chatSvc)
	dispatcher.SetDeviceTimeCallback(serialMgr.SetDeviceTime)
	serialMgr.RegisterHandler(dispatcher.HandlePacket)

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
	if err := appCfg.Load(ctx); err != nil {
		slog.Warn("failed to load app config", "error", err)
	}

	// Start serial manager
	if cfg.SerialDevice != "" {
		go func() {
			if err := serialMgr.Start(); err != nil {
				slog.Error("serial manager failed", "error", err)
			}
		}()
	} else {
		slog.Info("no serial device configured, serial disabled")
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

	<-sigCtx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serialMgr.Stop()
	hub.Stop()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	fmt.Println("DigiNode CC stopped.")
}
