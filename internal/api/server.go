package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/acars"
	"github.com/karamble/diginode-cc/internal/adsb"
	"github.com/karamble/diginode-cc/internal/audit"
	"github.com/karamble/diginode-cc/internal/alarms"
	"github.com/karamble/diginode-cc/internal/alerts"
	"github.com/karamble/diginode-cc/internal/auth"
	"github.com/karamble/diginode-cc/internal/chat"
	"github.com/karamble/diginode-cc/internal/commands"
	"github.com/karamble/diginode-cc/internal/config"
	"github.com/karamble/diginode-cc/internal/drones"
	"github.com/karamble/diginode-cc/internal/exports"
	"github.com/karamble/diginode-cc/internal/faa"
	"github.com/karamble/diginode-cc/internal/firewall"
	"github.com/karamble/diginode-cc/internal/geofences"
	"github.com/karamble/diginode-cc/internal/inventory"
	"github.com/karamble/diginode-cc/internal/mail"
	"github.com/karamble/diginode-cc/internal/mqtt"
	"github.com/karamble/diginode-cc/internal/nodes"
	"github.com/karamble/diginode-cc/internal/permissions"
	"github.com/karamble/diginode-cc/internal/ratelimit"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/sites"
	"github.com/karamble/diginode-cc/internal/targets"
	"github.com/karamble/diginode-cc/internal/tiles"
	"github.com/karamble/diginode-cc/internal/updates"
	"github.com/karamble/diginode-cc/internal/users"
	"github.com/karamble/diginode-cc/internal/webhooks"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Services bundles all domain services for the API server.
type Services struct {
	Auth      *auth.Service
	Users     *users.Service
	Sites     *sites.Service
	Nodes     *nodes.Service
	Drones    *drones.Service
	Chat      *chat.Service
	Commands  *commands.Service
	Alerts    *alerts.Service
	Geofences *geofences.Service
	Targets   *targets.Service
	Inventory *inventory.Service
	Webhooks  *webhooks.Service
	Alarms    *alarms.Service
	Firewall  *firewall.Service
	FAA       *faa.Service
	Exports   *exports.Service
	Mail      *mail.Service
	AppCfg      *config.AppConfig
	Permissions *permissions.Service
	Audit       *audit.Service
	ACARS       *acars.Service
	ADSB        *adsb.Service
	MQTT        *mqtt.Service
	Updates     *updates.Service
	Database    *database.DB
}

// DB returns the database handle for direct queries (admin operations).
func (s *Services) DB() *database.DB { return s.Database }

// Server is the HTTP API server.
type Server struct {
	cfg          *config.Config
	hub          *ws.Hub
	serialMgr    *serial.Manager
	svc          *Services
	tileCache    *tiles.TileCache
	rlDefault    *ratelimit.Limiter
	rlLogin      *ratelimit.Limiter
	rl2FA        *ratelimit.Limiter
	router       chi.Router
	upgrader     websocket.Upgrader
}

// NewServer creates a new API server.
func NewServer(cfg *config.Config, hub *ws.Hub, serialMgr *serial.Manager, svc *Services) *Server {
	s := &Server{
		cfg:       cfg,
		hub:       hub,
		serialMgr: serialMgr,
		svc:       svc,
		tileCache: tiles.NewTileCache("data/tiles", cfg.JawgAccessToken),
		rlDefault: ratelimit.New(cfg.RateLimitDefault, cfg.RateLimitDefaultTTL),
		rlLogin:   ratelimit.New(cfg.RateLimitLogin, cfg.RateLimitLoginTTL),
		rl2FA:     ratelimit.New(cfg.RateLimit2FA, cfg.RateLimit2FATTL),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins (nginx handles CORS)
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	s.router = s.setupRoutes()
	return s
}

// Router returns the configured HTTP router.
func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) setupRoutes() chi.Router {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Health check (no auth)
	r.Get("/api/health", s.handleHealth)
	r.Get("/healthz", s.handleHealth) // gotailme compat alias
	r.Get("/readyz", s.handleHealth)  // readiness probe alias

	// WebSocket endpoint (auth checked on connect)
	r.Get("/ws", s.handleWebSocket)

	// API routes
	r.Route("/api", func(r chi.Router) {
		r.Use(s.rlDefault.Middleware)

		// Auth routes (public, with stricter login rate limit)
		r.Group(func(r chi.Router) {
			r.Use(s.rlLogin.Middleware)
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/register", s.handleRegister)
			r.Post("/auth/forgot-password", s.handleForgotPassword)
			r.Post("/auth/reset-password", s.handleResetPassword)
		})

		// Tile proxy (no auth — Leaflet loads these as <img> src)
		r.Get("/tiles/{provider}/{z}/{x}/{y}", s.handleTileRequest)

		// Protected routes
		r.Group(func(r chi.Router) {
			r.Use(s.svc.Auth.Middleware)

			// Auth
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/legal-ack", s.handleLegalAck)
			r.Route("/auth/2fa", func(r chi.Router) {
				r.Use(s.rl2FA.Middleware)
				r.Post("/setup", s.handle2FASetup)
				r.Post("/verify", s.handle2FAVerify)
				r.Post("/confirm", s.handle2FAConfirm)
				r.Post("/disable", s.handle2FADisable)
				r.Post("/recovery/regenerate", s.handle2FARecoveryRegenerate)
			})

			// Users
			r.Route("/users", func(r chi.Router) {
				r.Get("/me", s.handleMe) // Alias for /auth/me
				r.Get("/", s.handleListUsers)
				r.Post("/", s.handleCreateUser)
				r.Get("/features", s.handleListFeatures)
				r.Post("/invite", s.handleInviteUser)
				r.Get("/{id}", s.handleGetUser)
				r.Put("/{id}", s.handleUpdateUser)
				r.Delete("/{id}", s.handleDeleteUser)
				r.Post("/{id}/unlock", s.handleUnlockUser)
				r.Patch("/{id}/permissions", s.handleUpdateUserPermissions)
				r.Patch("/{id}/sites", s.handleUpdateUserSites)
				r.Post("/{id}/password-reset", s.handleAdminPasswordReset)
				r.Get("/{id}/audit", s.handleGetUserAudit)
			})

			// Sites
			r.Route("/sites", func(r chi.Router) {
				r.Get("/", s.handleListSites)
				r.Post("/", s.handleCreateSite)
				r.Get("/{id}", s.handleGetSite)
				r.Put("/{id}", s.handleUpdateSite)
				r.Delete("/{id}", s.handleDeleteSite)
			})

			// Nodes
			r.Route("/nodes", func(r chi.Router) {
				r.Get("/", s.handleListNodes)
				r.Post("/clear", s.handleClearNodes)
				r.Get("/{id}", s.handleGetNode)
				r.Get("/{id}/positions", s.handleGetNodePositions)
				r.Put("/{id}", s.handleUpdateNode)
				r.Delete("/{id}", s.handleDeleteNode)
			})

			// Drones
			r.Route("/drones", func(r chi.Router) {
				r.Get("/", s.handleListDrones)
				r.Post("/clear", s.handleClearDrones)
				r.Get("/{id}", s.handleGetDrone)
				r.Put("/{id}/status", s.handleUpdateDroneStatus)
				r.Get("/{id}/detections", s.handleGetDroneDetections)
				r.Delete("/{id}", s.handleDeleteDrone)
			})

			// Commands
			r.Route("/commands", func(r chi.Router) {
				r.Get("/", s.handleListCommands)
				r.Post("/", s.handleCreateCommand)
				r.Get("/types", s.handleListCommandTypes)
				r.Get("/{id}", s.handleGetCommand)
				r.Delete("/{id}", s.handleDeleteCommand)
			})

			// Chat
			r.Route("/chat", func(r chi.Router) {
				r.Get("/messages", s.handleGetChatMessages)
				r.Post("/send", s.handleSendChatMessage)
				r.Delete("/messages", s.handleClearChatMessages)
			})

			// Alerts
			r.Route("/alerts", func(r chi.Router) {
				r.Get("/rules", s.handleListAlertRules)
				r.Post("/rules", s.handleCreateAlertRule)
				r.Put("/rules/{id}", s.handleUpdateAlertRule)
				r.Delete("/rules/{id}", s.handleDeleteAlertRule)
				r.Get("/events", s.handleListAlertEvents)
				r.Post("/events/{id}/acknowledge", s.handleAcknowledgeAlert)
			})

			// Geofences
			r.Route("/geofences", func(r chi.Router) {
				r.Get("/", s.handleListGeofences)
				r.Post("/", s.handleCreateGeofence)
				r.Put("/{id}", s.handleUpdateGeofence)
				r.Delete("/{id}", s.handleDeleteGeofence)
			})

			// Targets
			r.Route("/targets", func(r chi.Router) {
				r.Get("/", s.handleListTargets)
				r.Post("/", s.handleCreateTarget)
				r.Post("/clear", s.handleClearTargets)
				r.Put("/{id}", s.handleUpdateTarget)
				r.Delete("/{id}", s.handleDeleteTarget)
				r.Get("/{id}/positions", s.handleGetTargetPositions)
				r.Post("/{id}/resolve", s.handleResolveTarget)
			})

			// Inventory
			r.Route("/inventory", func(r chi.Router) {
				r.Get("/", s.handleListInventory)
				r.Post("/clear", s.handleClearInventory)
				r.Post("/{mac}/promote", s.handlePromoteToTarget)
				r.Put("/{id}", s.handleUpdateInventoryDevice)
			})

			// Webhooks
			r.Route("/webhooks", func(r chi.Router) {
				r.Get("/", s.handleListWebhooks)
				r.Post("/", s.handleCreateWebhook)
				r.Put("/{id}", s.handleUpdateWebhook)
				r.Delete("/{id}", s.handleDeleteWebhook)
				r.Post("/{id}/test", s.handleTestWebhook)
			})

			// Config
			r.Route("/config", func(r chi.Router) {
				r.Get("/", s.handleGetConfig)
				r.Put("/", s.handleUpdateConfig)
				r.Get("/{key}", s.handleGetConfigKey)
				r.Put("/{key}", s.handleUpdateConfigKey)
			})

			// Alarms
			r.Route("/alarms", func(r chi.Router) {
				r.Get("/", s.handleListAlarms)
				r.Post("/", s.handleCreateAlarm)
				r.Put("/{id}", s.handleUpdateAlarm)
				r.Delete("/{id}", s.handleDeleteAlarm)
				r.Post("/sounds/{level}", s.handleUploadAlarmSound)
				r.Delete("/sounds/{level}", s.handleDeleteAlarmSound)
			})

			// Firewall
			r.Route("/firewall", func(r chi.Router) {
				r.Get("/", s.handleFirewallStatus)
				r.Put("/", s.handleUpdateFirewallConfig)
				r.Get("/rules", s.handleListFirewallRules)
				r.Post("/rules", s.handleCreateFirewallRule)
				r.Delete("/rules/{id}", s.handleDeleteFirewallRule)
				r.Get("/jailed", s.handleListJailedIPs)
				r.Delete("/jailed/{id}", s.handleUnjailIP)
				r.Get("/logs", s.handleFirewallLogs)
			})

			// FAA
			r.Route("/faa", func(r chi.Router) {
				r.Get("/lookup/{serial}", s.handleFAALookup)
				r.Post("/import", s.handleFAAImport)
				r.Get("/status", s.handleFAAStatus)
				r.Post("/sync", s.handleFAASync)
				r.Post("/upload", s.handleFAAUpload)
			})

			// Tiles (preload management)
			r.Route("/tiles", func(r chi.Router) {
				r.Post("/preload", s.handleTilePreload)
				r.Get("/preload/status", s.handleTilePreloadStatus)
				r.Post("/preload/cancel", s.handleTilePreloadCancel)
			})

			// Admin / Data Management (ADMIN role only)
			r.Route("/admin", func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleAdmin))
				r.Post("/clear-detections", s.handleClearDetectionData)
				r.Post("/clear-operational", s.handleClearOperationalData)
				r.Post("/prune", s.handlePruneOldData)
				r.Post("/factory-reset", s.handleFactoryReset)
				r.Delete("/tiles-cache", s.handleClearTileCache)
			})

			// Exports
			r.Route("/exports", func(r chi.Router) {
				r.Get("/drones", s.handleExportDrones)
				r.Get("/nodes", s.handleExportNodes)
				r.Get("/alerts", s.handleExportAlerts)
				r.Get("/{type}", s.handleExportByType)
			})

			// Serial
			r.Route("/serial", func(r chi.Router) {
				r.Get("/ports", s.handleListSerialPorts)
				r.Get("/protocols", s.handleListSerialProtocols)
				r.Get("/status", s.handleSerialStatus)
				r.Get("/state", s.handleSerialStatus) // CC PRO compat alias
				r.Get("/text-messages", s.handleGetTextMessages)
				r.Get("/device-time", s.handleGetDeviceTime)
				r.Get("/config", s.handleGetSerialConfig)
				r.Put("/config", s.handleUpdateSerialConfig)
				r.Post("/config/reset", s.handleResetSerialConfig)
				r.Post("/connect", s.handleSerialConnect)
				r.Post("/disconnect", s.handleSerialDisconnect)
				r.Post("/refresh", s.handleRefreshNodes)
				r.Post("/text-message", s.handleSendSerialTextMessage)
				r.Post("/text-alert", s.handleSendSerialTextAlert)
				r.Post("/position", s.handleSendSerialPosition)
				r.Post("/device-metrics", s.handleSendSerialDeviceMetrics)
				r.Post("/display-config", s.handleSendSerialDisplayConfig)
				r.Post("/bluetooth-config", s.handleSendSerialBluetoothConfig)
				r.Post("/shutdown", s.handleSendSerialShutdown)
				r.Post("/nodedb-reset", s.handleSendSerialNodedbReset)
				r.Post("/wake", s.handleWakeDevice)
				r.Get("/config", s.handleGetRadioConfig)
				r.Post("/simulate", s.handleSerialSimulate)
			})

			// ADS-B
			r.Route("/adsb", func(r chi.Router) {
				r.Get("/status", s.handleADSBStatus)
				r.Get("/tracks", s.handleADSBTracks)
				r.Get("/config", s.handleGetADSBConfig)
				r.Put("/config", s.handleUpdateADSBConfig)
				r.Get("/log", s.handleADSBLog)
				r.Delete("/log", s.handleClearADSBLog)
				r.Post("/database/upload", s.handleADSBDatabaseUpload)
				r.Post("/opensky/credentials", s.handleADSBOpenSkyCredentials)
				r.Get("/alerts/rules", s.handleListADSBAlertRules)
				r.Post("/alerts/rules", s.handleCreateADSBAlertRule)
				r.Put("/alerts/rules/{id}", s.handleUpdateADSBAlertRule)
				r.Delete("/alerts/rules/{id}", s.handleDeleteADSBAlertRule)
			})

			// MQTT
			r.Route("/mqtt", func(r chi.Router) {
				r.Get("/sites", s.handleListMQTTSites)
				r.Put("/sites/{siteId}", s.handleUpdateMQTTSite)
				r.Get("/sites-status", s.handleMQTTSitesStatus)
				r.Post("/sites/{siteId}/test", s.handleTestMQTTSite)
				r.Post("/sites/{siteId}/restart", s.handleRestartMQTTSite)
				r.Get("/config", s.handleGetMQTTConfig)
				r.Put("/config", s.handleUpdateMQTTConfig)
			})

			// Updates
			r.Route("/updates", func(r chi.Router) {
				r.Get("/check", s.handleCheckUpdate)
				r.Get("/status", s.handleUpdateStatus)
				r.Post("/trigger", s.handleTriggerUpdate)
				r.Get("/history", s.handleUpdateHistory)
				r.Post("/rollback/{id}", s.handleRollbackUpdate)
			})

			// ACARS
			r.Route("/acars", func(r chi.Router) {
				r.Get("/status", s.handleACARSStatus)
				r.Get("/messages", s.handleGetACARSMessages)
				r.Delete("/messages", s.handleClearACARSMessages)
				r.Post("/config", s.handleUpdateACARSConfig)
			})

			// TAK
			r.Route("/tak", func(r chi.Router) {
				r.Get("/config", s.handleGetTAKConfig)
				r.Put("/config", s.handleUpdateTAKConfig)
				r.Post("/reload", s.handleTAKReload)
				r.Post("/send", s.handleTAKSend)
			})

			// OUI
			r.Route("/oui", func(r chi.Router) {
				r.Get("/stats", s.handleOUIStats)
				r.Get("/cache", s.handleOUICache)
				r.Post("/import", s.handleOUIImport)
				r.Get("/export", s.handleOUIExport)
				r.Get("/resolve/{mac}", s.handleOUIResolve)
			})

			// Audit
			r.Get("/audit", s.handleListAuditLogs)

			// System
			r.Get("/system/info", s.handleSystemInfo)
			r.Post("/system/update", s.handleSystemUpdate)
		})
	})

	// Serve static frontend files with SPA fallback
	staticDir := http.Dir("web/dist")
	fileServer := http.FileServer(staticDir)
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Check if the file exists on disk
		if f, err := staticDir.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}))

	return r
}

// Health endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
		"serial": map[string]interface{}{
			"connected": s.serialMgr.IsConnected(),
			"device":    s.cfg.SerialDevice,
		},
		"websocket": map[string]interface{}{
			"clients": s.hub.ClientCount(),
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// WebSocket upgrade handler — sends init event with current state.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Validate JWT before upgrading — check query param "token" or Authorization header
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
			tokenStr = auth[7:]
		}
	}
	if tokenStr != "" {
		if _, err := s.svc.Auth.ValidateToken(tokenStr); err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "error", err)
		return
	}

	client := ws.NewClient(s.hub, conn)
	s.hub.Register(client)

	// Send init event with current state
	initPayload := ws.Event{
		Type: ws.EventInit,
		Payload: map[string]interface{}{
			"nodes":     s.svc.Nodes.GetAll(),
			"drones":    s.svc.Drones.GetAll(),
			"geofences": s.svc.Geofences.GetAll(),
			"serial": map[string]interface{}{
				"connected": s.serialMgr.IsConnected(),
				"device":    s.cfg.SerialDevice,
			},
		},
	}
	data, err := json.Marshal(initPayload)
	if err == nil {
		client.Send(data)
	}

	go client.WritePump()
	go client.ReadPump()
}

// Serial status
func (s *Server) handleSerialStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connected": s.serialMgr.IsConnected(),
		"device":    s.cfg.SerialDevice,
		"baud":      s.cfg.SerialBaud,
	})
}

// System info
func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version": "dev",
		"uptime":  time.Since(startTime).String(),
	})
}

var startTime = time.Now()

// JSON helpers
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}
