package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"github.com/karamble/diginode-cc/internal/config"
	"github.com/karamble/diginode-cc/internal/database"
	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

// Server is the HTTP API server.
type Server struct {
	cfg       *config.Config
	db        *database.DB
	hub       *ws.Hub
	serialMgr *serial.Manager
	router    chi.Router
	upgrader  websocket.Upgrader
}

// NewServer creates a new API server.
func NewServer(cfg *config.Config, db *database.DB, hub *ws.Hub, serialMgr *serial.Manager) *Server {
	s := &Server{
		cfg:       cfg,
		db:        db,
		hub:       hub,
		serialMgr: serialMgr,
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

	// WebSocket endpoint (auth checked on connect)
	r.Get("/ws", s.handleWebSocket)

	// API routes
	r.Route("/api", func(r chi.Router) {
		// Auth routes (public)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/register", s.handleRegister)
		r.Post("/auth/forgot-password", s.handleForgotPassword)
		r.Post("/auth/reset-password", s.handleResetPassword)

		// Protected routes
		r.Group(func(r chi.Router) {
			// TODO: Add JWT auth middleware
			// r.Use(s.authMiddleware)

			// Auth
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/2fa/setup", s.handle2FASetup)
			r.Post("/auth/2fa/verify", s.handle2FAVerify)

			// Users
			r.Route("/users", func(r chi.Router) {
				r.Get("/", s.handleListUsers)
				r.Post("/", s.handleCreateUser)
				r.Get("/{id}", s.handleGetUser)
				r.Put("/{id}", s.handleUpdateUser)
				r.Delete("/{id}", s.handleDeleteUser)
				r.Post("/invite", s.handleInviteUser)
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
				r.Get("/{id}", s.handleGetNode)
				r.Get("/{id}/positions", s.handleGetNodePositions)
				r.Put("/{id}", s.handleUpdateNode)
			})

			// Drones
			r.Route("/drones", func(r chi.Router) {
				r.Get("/", s.handleListDrones)
				r.Get("/{id}", s.handleGetDrone)
				r.Put("/{id}/status", s.handleUpdateDroneStatus)
				r.Get("/{id}/detections", s.handleGetDroneDetections)
			})

			// Commands
			r.Route("/commands", func(r chi.Router) {
				r.Get("/", s.handleListCommands)
				r.Post("/", s.handleCreateCommand)
				r.Get("/{id}", s.handleGetCommand)
				r.Delete("/{id}", s.handleDeleteCommand)
			})

			// Chat
			r.Route("/chat", func(r chi.Router) {
				r.Get("/messages", s.handleGetChatMessages)
				r.Post("/send", s.handleSendChatMessage)
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
				r.Put("/{id}", s.handleUpdateTarget)
				r.Delete("/{id}", s.handleDeleteTarget)
				r.Get("/{id}/positions", s.handleGetTargetPositions)
			})

			// Inventory
			r.Route("/inventory", func(r chi.Router) {
				r.Get("/", s.handleListInventory)
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
			})

			// Firewall
			r.Route("/firewall", func(r chi.Router) {
				r.Get("/rules", s.handleListFirewallRules)
				r.Post("/rules", s.handleCreateFirewallRule)
				r.Delete("/rules/{id}", s.handleDeleteFirewallRule)
			})

			// FAA
			r.Route("/faa", func(r chi.Router) {
				r.Get("/lookup/{serial}", s.handleFAALookup)
				r.Post("/import", s.handleFAAImport)
			})

			// Exports
			r.Route("/exports", func(r chi.Router) {
				r.Get("/drones", s.handleExportDrones)
				r.Get("/nodes", s.handleExportNodes)
				r.Get("/alerts", s.handleExportAlerts)
			})

			// Serial
			r.Route("/serial", func(r chi.Router) {
				r.Get("/status", s.handleSerialStatus)
				r.Get("/state", s.handleSerialStatus) // CC PRO compat alias
				r.Get("/text-messages", s.handleGetTextMessages)
				r.Get("/device-time", s.handleGetDeviceTime)
				r.Get("/config", s.handleGetSerialConfig)
				r.Put("/config", s.handleUpdateSerialConfig)
				r.Post("/connect", s.handleSerialConnect)
				r.Post("/disconnect", s.handleSerialDisconnect)
				r.Post("/text-message", s.handleSendSerialTextMessage)
				r.Post("/text-alert", s.handleSendSerialTextAlert)
				r.Post("/position", s.handleSendSerialPosition)
				r.Post("/device-metrics", s.handleSendSerialDeviceMetrics)
				r.Post("/display-config", s.handleSendSerialDisplayConfig)
				r.Post("/shutdown", s.handleSendSerialShutdown)
				r.Post("/simulate", s.handleSerialSimulate)
			})

			// System
			r.Get("/system/info", s.handleSystemInfo)
			r.Post("/system/update", s.handleSystemUpdate)
		})
	})

	// Serve static frontend files
	fileServer := http.FileServer(http.Dir("web/dist"))
	r.Handle("/*", fileServer)

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

// WebSocket upgrade handler
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "error", err)
		return
	}

	client := ws.NewClient(s.hub, conn)
	s.hub.Register(client)

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
