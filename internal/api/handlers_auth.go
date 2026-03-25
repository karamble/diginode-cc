package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/auth"
	"github.com/karamble/diginode-cc/internal/permissions"
	"github.com/karamble/diginode-cc/internal/users"
)

// --- Request types ---

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type verify2FARequest struct {
	Code string `json:"code"`
}

// --- Response types ---

// loginResponse is CC PRO compatible for gotailme integration.
type loginResponse struct {
	Token             string       `json:"token"`
	User              userResponse `json:"user"`
	LegalAccepted     bool         `json:"legalAccepted"`
	TwoFactorRequired bool         `json:"twoFactorRequired"`
	Disclaimer        string       `json:"disclaimer,omitempty"`
}

type userResponse struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name,omitempty"`
	Role               auth.Role  `json:"role"`
	TOTPEnabled        bool       `json:"twoFactorEnabled"`
	MustChangePassword bool       `json:"mustChangePassword"`
	TOSAccepted        bool       `json:"legalAccepted"`
	LastLogin          *time.Time `json:"lastLoginAt,omitempty"`
}

// buildUserResponse converts an auth.User to the CC PRO compatible userResponse.
func buildUserResponse(u *auth.User) userResponse {
	return userResponse{
		ID:                 u.ID,
		Email:              u.Email,
		Name:               u.Name,
		Role:               u.Role,
		TOTPEnabled:        u.TOTPEnabled,
		MustChangePassword: u.MustChangePassword,
		TOSAccepted:        u.TOSAccepted,
		LastLogin:          u.LastLogin,
	}
}

// --- Handlers ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	clientIP := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		clientIP = strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}

	token, user, err := s.svc.Auth.Login(r.Context(), req.Email, req.Password, clientIP)
	if err != nil {
		if s.svc.Audit != nil {
			s.svc.Audit.Log(r.Context(), "", "AUTH_LOGIN_FAILED", "user", "", clientIP,
				map[string]interface{}{"email": req.Email})
		}
		if errors.Is(err, auth.ErrAccountLocked) {
			writeError(w, http.StatusTooManyRequests, "account is temporarily locked due to too many failed login attempts")
			return
		}
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	if s.svc.Audit != nil {
		s.svc.Audit.Log(r.Context(), user.ID, "AUTH_LOGIN", "user", user.ID, clientIP, nil)
	}

	// CC PRO compatible response format
	writeJSON(w, http.StatusOK, loginResponse{
		Token:             token,
		User:              buildUserResponse(user),
		LegalAccepted:     user.TOSAccepted,
		TwoFactorRequired: user.TOTPEnabled,
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := s.svc.Auth.Register(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// JWT is stateless — client discards the token.
	// This endpoint exists for CC PRO API compatibility.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	user, err := s.svc.Auth.GetUser(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, buildUserResponse(user))
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Always return success to prevent email enumeration.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "if an account with that email exists, a reset link has been sent",
	})

	// Generate reset token and send email in background (after response).
	if s.svc.Mail != nil && s.svc.Mail.IsConfigured() && req.Email != "" {
		go func() {
			ctx := r.Context()
			// Look up user by email
			var userID string
			err := s.svc.Auth.DB().Pool.QueryRow(ctx,
				`SELECT id FROM users WHERE email = $1`, req.Email).Scan(&userID)
			if err != nil {
				return // User not found — silent (prevent enumeration)
			}

			// Generate secure token
			tokenBytes := make([]byte, 32)
			if _, err := rand.Read(tokenBytes); err != nil {
				slog.Error("failed to generate reset token", "error", err)
				return
			}
			token := hex.EncodeToString(tokenBytes)
			expiresAt := time.Now().Add(s.svc.Auth.PasswordResetExpiry())

			// Store token in password_resets table
			_, err = s.svc.Auth.DB().Pool.Exec(ctx, `
				INSERT INTO password_resets (user_id, token, expires_at)
				VALUES ($1, $2, $3)`, userID, token, expiresAt)
			if err != nil {
				slog.Error("failed to store reset token", "error", err)
				return
			}

			// Build reset URL and send email
			host := r.Header.Get("X-Forwarded-Host")
			if host == "" {
				host = r.Host
			}
			scheme := "https"
			if r.TLS == nil {
				scheme = "http"
			}
			resetURL := fmt.Sprintf("%s://%s/reset-password?token=%s", scheme, host, token)

			if err := s.svc.Mail.SendPasswordReset(req.Email, resetURL); err != nil {
				slog.Error("failed to send password reset email", "email", req.Email, "error", err)
			}
		}()
	}
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Token == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "token and password are required")
		return
	}

	if err := s.svc.Auth.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			writeError(w, http.StatusBadRequest, "invalid or expired reset token")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "password has been reset"})
}

func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	secret, url, err := s.svc.Auth.SetupTOTP(r.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to setup 2FA")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"url":    url,
	})
}

func (s *Server) handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req verify2FARequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	err := s.svc.Auth.VerifyTOTP(r.Context(), claims.UserID, req.Code)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidTOTP) {
			writeError(w, http.StatusUnauthorized, "invalid 2FA code")
			return
		}
		if errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "2FA verification failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "2FA enabled successfully",
	})
}

// handleLegalAck marks the current user as having accepted the Terms of Service.
func (s *Server) handleLegalAck(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Accept empty body or {}
	_ = readJSON(r, &struct{}{})

	if err := s.svc.Users.AcceptTOS(r.Context(), claims.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to accept terms of service")
		return
	}

	// Return updated user
	user, err := s.svc.Auth.GetUser(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, buildUserResponse(user))
}

// handle2FAConfirm confirms 2FA setup by validating a code against the temp secret.
// This is an alias for handle2FAVerify — VerifyTOTP already validates the code and enables 2FA.
func (s *Server) handle2FAConfirm(w http.ResponseWriter, r *http.Request) {
	s.handle2FAVerify(w, r)
}

// handle2FADisable disables 2FA for the current user after verifying a TOTP code or password.
func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Must provide either TOTP code or password
	if req.Code == "" && req.Password == "" {
		writeError(w, http.StatusBadRequest, "code or password is required")
		return
	}

	// Verify via TOTP code if provided (validate only, don't enable)
	if req.Code != "" {
		err := s.svc.Auth.ValidateTOTP(r.Context(), claims.UserID, req.Code)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidTOTP) {
				writeError(w, http.StatusUnauthorized, "invalid 2FA code")
				return
			}
			writeError(w, http.StatusInternalServerError, "verification failed")
			return
		}
	} else {
		// Verify via password
		err := s.svc.Auth.VerifyPassword(r.Context(), claims.UserID, req.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				writeError(w, http.StatusUnauthorized, "invalid password")
				return
			}
			writeError(w, http.StatusInternalServerError, "verification failed")
			return
		}
	}

	if err := s.svc.Auth.DisableTOTP(r.Context(), claims.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable 2FA")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "2FA disabled successfully",
	})
}

// handle2FARecoveryRegenerate generates new recovery codes for the current user.
func (s *Server) handle2FARecoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	codes, err := s.svc.Auth.RegenerateRecoveryCodes(r.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"recoveryCodes": codes,
	})
}

// handleUnlockUser clears the lockout state for a user (admin only).
func (s *Server) handleUnlockUser(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if claims.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	id := chi.URLParam(r, "id")
	if err := s.svc.Auth.UnlockUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unlock user")
		return
	}

	if s.svc.Audit != nil {
		s.svc.Audit.Log(r.Context(), claims.UserID, "USER_UNLOCK", "user", id, "", nil)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "user unlocked"})
}

// handleUpdateUserPermissions sets feature-level permissions for a user.
func (s *Server) handleUpdateUserPermissions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Features []string `json:"features"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	features := make([]permissions.Feature, len(body.Features))
	for i, f := range body.Features {
		features[i] = permissions.Feature(f)
	}

	if err := s.svc.Permissions.SetUserFeatures(r.Context(), id, features); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update permissions")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleUpdateUserSites sets site access for a user.
func (s *Server) handleUpdateUserSites(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		SiteAccess []users.SiteAccess `json:"siteAccess"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Users.SetSiteAccess(r.Context(), id, body.SiteAccess); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update site access")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAdminPasswordReset triggers a password reset for a user (admin action).
func (s *Server) handleAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if claims.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	id := chi.URLParam(r, "id")

	token, err := s.svc.Auth.GeneratePasswordResetToken(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to generate reset token")
		return
	}

	// Build reset URL
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	resetURL := fmt.Sprintf("%s://%s/reset-password?token=%s", scheme, host, token)

	// Send email if configured
	if s.svc.Mail != nil && s.svc.Mail.IsConfigured() {
		// Look up the target user's email
		targetUser, err := s.svc.Auth.GetUser(r.Context(), id)
		if err == nil {
			adminName := "An administrator"
			adminUser, err := s.svc.Auth.GetUser(r.Context(), claims.UserID)
			if err == nil && adminUser.Name != "" {
				adminName = adminUser.Name
			}
			go func() {
				if err := s.svc.Mail.SendPasswordResetAdmin(targetUser.Email, resetURL, adminName); err != nil {
					slog.Error("failed to send admin password reset email", "email", targetUser.Email, "error", err)
				}
			}()
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"message":  "password reset initiated",
		"resetUrl": resetURL,
		"token":    token,
	})
}

// handleGetUserAudit returns recent audit log entries for a user.
func (s *Server) handleGetUserAudit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	entries, err := s.svc.Users.GetAuditLogs(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch audit logs")
		return
	}

	if entries == nil {
		entries = []*users.AuditEntry{}
	}

	writeJSON(w, http.StatusOK, entries)
}
