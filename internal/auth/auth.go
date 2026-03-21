package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/karamble/diginode-cc/internal/database"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrTOTPRequired       = errors.New("2FA code required")
	ErrInvalidTOTP        = errors.New("invalid 2FA code")
	ErrTOSNotAccepted     = errors.New("terms of service not accepted")
	ErrAccountLocked      = errors.New("account is temporarily locked")
)

// Role defines user permission levels.
type Role string

const (
	RoleAdmin    Role = "ADMIN"
	RoleOperator Role = "OPERATOR"
	RoleAnalyst  Role = "ANALYST"
	RoleViewer   Role = "VIEWER"
)

// User represents an authenticated user.
type User struct {
	ID                  string     `json:"id"`
	Email               string     `json:"email"`
	Name                string     `json:"name,omitempty"`
	Role                Role       `json:"role"`
	TOTPEnabled         bool       `json:"totpEnabled"`
	MustChangePassword  bool       `json:"mustChangePassword"`
	TOSAccepted         bool       `json:"tosAccepted"`
	LastLogin           *time.Time `json:"lastLogin,omitempty"`
	FailedLoginAttempts int        `json:"-"`
	LockedUntil         *time.Time `json:"-"`
	LastLoginIP         string     `json:"-"`
}

// Claims represents JWT token claims.
type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Role   Role   `json:"role"`
}

// Service handles authentication and user management.
type Service struct {
	db        *database.DB
	jwtSecret []byte
}

// DB returns the underlying database for direct queries (e.g., password reset tokens).
func (s *Service) DB() *database.DB { return s.db }

// NewService creates a new auth service.
func NewService(db *database.DB, jwtSecret string) *Service {
	return &Service{
		db:        db,
		jwtSecret: []byte(jwtSecret),
	}
}

// Login authenticates a user and returns a JWT token.
func (s *Service) Login(ctx context.Context, email, password, clientIP string) (string, *User, error) {
	var (
		id                  string
		passwordHash        string
		name                string
		role                string
		totpEnabled         bool
		totpSecret          string
		mustChange          bool
		tosAccepted         bool
		lastLogin           *time.Time
		failedLoginAttempts int
		lockedUntil         *time.Time
	)

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, password_hash, COALESCE(name, ''), role, totp_enabled, COALESCE(totp_secret, ''),
			must_change_password, tos_accepted, last_login,
			COALESCE(failed_login_attempts, 0), locked_until
		FROM users WHERE email = $1`, email).Scan(
		&id, &passwordHash, &name, &role, &totpEnabled, &totpSecret,
		&mustChange, &tosAccepted, &lastLogin,
		&failedLoginAttempts, &lockedUntil,
	)
	if err != nil {
		slog.Error("login query failed", "email", email, "error", err)
		return "", nil, ErrInvalidCredentials
	}

	// Check account lockout
	if lockedUntil != nil && time.Now().Before(*lockedUntil) {
		return "", nil, ErrAccountLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		// Increment failed attempts and lock if threshold reached
		_, _ = s.db.Pool.Exec(ctx, `
			UPDATE users SET failed_login_attempts = COALESCE(failed_login_attempts, 0) + 1,
			locked_at = CASE WHEN COALESCE(failed_login_attempts, 0) >= 4 THEN NOW() ELSE locked_at END,
			locked_until = CASE WHEN COALESCE(failed_login_attempts, 0) >= 4 THEN NOW() + interval '15 minutes' ELSE locked_until END
			WHERE id = $1`, id)
		return "", nil, ErrInvalidCredentials
	}

	user := &User{
		ID:                 id,
		Email:              email,
		Name:               name,
		Role:               Role(role),
		TOTPEnabled:        totpEnabled,
		MustChangePassword: mustChange,
		TOSAccepted:        tosAccepted,
		LastLogin:          lastLogin,
	}

	token, err := s.generateToken(user)
	if err != nil {
		return "", nil, err
	}

	// Reset failed attempts and record successful login
	_, _ = s.db.Pool.Exec(ctx, `
		UPDATE users SET last_login = NOW(), failed_login_attempts = 0,
		locked_at = NULL, locked_until = NULL, last_login_ip = $2
		WHERE id = $1`, id, clientIP)

	return token, user, nil
}

// GetUser looks up a user by ID and returns the public User struct.
func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	var (
		email      string
		name       string
		role       string
		totpEn     bool
		mustChange bool
		tosAcc     bool
		lastLogin  *time.Time
	)

	err := s.db.Pool.QueryRow(ctx, `
		SELECT email, name, role, totp_enabled, must_change_password,
			tos_accepted, last_login
		FROM users WHERE id = $1`, userID).Scan(
		&email, &name, &role, &totpEn, &mustChange, &tosAcc, &lastLogin,
	)
	if err != nil {
		return nil, ErrUserNotFound
	}

	return &User{
		ID:                 userID,
		Email:              email,
		Name:               name,
		Role:               Role(role),
		TOTPEnabled:        totpEn,
		MustChangePassword: mustChange,
		TOSAccepted:        tosAcc,
		LastLogin:          lastLogin,
	}, nil
}

// Register creates a new user account.
func (s *Service) Register(ctx context.Context, email, password, name string) (*User, error) {
	// Check if email is taken
	var exists bool
	err := s.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, email).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailTaken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// First user gets ADMIN role
	var count int
	_ = s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	role := RoleViewer
	if count == 0 {
		role = RoleAdmin
	}

	var id string
	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, email, string(hash), name, string(role)).Scan(&id)
	if err != nil {
		return nil, err
	}

	return &User{
		ID:    id,
		Email: email,
		Name:  name,
		Role:  role,
	}, nil
}

// ValidateToken validates a JWT token and returns the claims.
func (s *Service) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// SetupTOTP generates a TOTP secret for a user.
func (s *Service) SetupTOTP(ctx context.Context, userID string) (string, string, error) {
	var email string
	err := s.db.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	if err != nil {
		return "", "", ErrUserNotFound
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "DigiNode CC",
		AccountName: email,
	})
	if err != nil {
		return "", "", err
	}

	_, err = s.db.Pool.Exec(ctx, `UPDATE users SET totp_secret = $1 WHERE id = $2`, key.Secret(), userID)
	if err != nil {
		return "", "", err
	}

	return key.Secret(), key.URL(), nil
}

// VerifyTOTP validates a TOTP code and enables 2FA if valid.
func (s *Service) VerifyTOTP(ctx context.Context, userID, code string) error {
	var secret string
	err := s.db.Pool.QueryRow(ctx, `SELECT totp_secret FROM users WHERE id = $1`, userID).Scan(&secret)
	if err != nil {
		return ErrUserNotFound
	}

	if !totp.Validate(code, secret) {
		return ErrInvalidTOTP
	}

	_, err = s.db.Pool.Exec(ctx, `UPDATE users SET totp_enabled = true WHERE id = $1`, userID)
	if err != nil {
		return err
	}

	slog.Info("2FA enabled", "userID", userID)
	return nil
}

func (s *Service) generateToken(user *User) (string, error) {
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "diginode-cc",
			Subject:   user.ID,
		},
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// ResetPassword validates a reset token and sets a new password.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	var userID string
	var expiresAt time.Time
	err := s.db.Pool.QueryRow(ctx, `
		SELECT user_id, expires_at FROM password_resets
		WHERE token = $1 AND used = false`, token).Scan(&userID, &expiresAt)
	if err != nil {
		return ErrInvalidToken
	}
	if time.Now().After(expiresAt) {
		return ErrInvalidToken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = s.db.Pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, string(hash))
	if err != nil {
		return err
	}

	// Mark token as used
	_, _ = s.db.Pool.Exec(ctx, `UPDATE password_resets SET used = true WHERE token = $1`, token)

	slog.Info("password reset completed", "userID", userID)
	return nil
}

// ValidateTOTP checks a TOTP code without enabling 2FA (for verification-only flows).
func (s *Service) ValidateTOTP(ctx context.Context, userID, code string) error {
	var secret string
	err := s.db.Pool.QueryRow(ctx, `SELECT totp_secret FROM users WHERE id = $1`, userID).Scan(&secret)
	if err != nil {
		return ErrUserNotFound
	}

	if !totp.Validate(code, secret) {
		return ErrInvalidTOTP
	}

	return nil
}

// DisableTOTP disables 2FA for a user and clears the secret.
func (s *Service) DisableTOTP(ctx context.Context, userID string) error {
	_, err := s.db.Pool.Exec(ctx, `UPDATE users SET totp_enabled = false, totp_secret = NULL WHERE id = $1`, userID)
	if err != nil {
		return err
	}
	slog.Info("2FA disabled", "userID", userID)
	return nil
}

// VerifyPassword checks a user's current password.
func (s *Service) VerifyPassword(ctx context.Context, userID, password string) error {
	var hash string
	err := s.db.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if err != nil {
		return ErrUserNotFound
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// RegenerateRecoveryCodes generates new recovery codes for a user.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	// Verify user exists and has 2FA enabled
	var totpEnabled bool
	err := s.db.Pool.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id = $1`, userID).Scan(&totpEnabled)
	if err != nil {
		return nil, ErrUserNotFound
	}
	if !totpEnabled {
		return nil, errors.New("2FA is not enabled")
	}

	codes := make([]string, 8)
	for i := range codes {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("failed to generate recovery code: %w", err)
		}
		codes[i] = hex.EncodeToString(b)
	}

	// Store hashed recovery codes
	hashedCodes := make([]string, len(codes))
	for i, code := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		hashedCodes[i] = string(h)
	}

	_, err = s.db.Pool.Exec(ctx,
		`UPDATE users SET two_factor_recovery_codes = $1 WHERE id = $2`,
		hashedCodes, userID)
	if err != nil {
		return nil, err
	}

	slog.Info("recovery codes regenerated", "userID", userID)
	return codes, nil
}

// UnlockUser clears lockout state for a user (admin action).
func (s *Service) UnlockUser(ctx context.Context, userID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE users SET locked_at = NULL, locked_until = NULL, failed_login_attempts = 0
		WHERE id = $1`, userID)
	return err
}

// GeneratePasswordResetToken creates a password reset token for a user (admin-initiated).
func (s *Service) GeneratePasswordResetToken(ctx context.Context, userID string) (string, error) {
	// Verify user exists
	var email string
	err := s.db.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	if err != nil {
		return "", ErrUserNotFound
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate reset token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(1 * time.Hour)

	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO password_resets (user_id, token, expires_at)
		VALUES ($1, $2, $3)`, userID, token, expiresAt)
	if err != nil {
		return "", err
	}

	return token, nil
}

// HashPassword hashes a password with bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
