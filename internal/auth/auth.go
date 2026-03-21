package auth

import (
	"context"
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
	ID                 string    `json:"id"`
	Email              string    `json:"email"`
	Name               string    `json:"name,omitempty"`
	Role               Role      `json:"role"`
	TOTPEnabled        bool      `json:"totpEnabled"`
	MustChangePassword bool      `json:"mustChangePassword"`
	TOSAccepted        bool      `json:"tosAccepted"`
	LastLogin          time.Time `json:"lastLogin,omitempty"`
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

// NewService creates a new auth service.
func NewService(db *database.DB, jwtSecret string) *Service {
	return &Service{
		db:        db,
		jwtSecret: []byte(jwtSecret),
	}
}

// Login authenticates a user and returns a JWT token.
func (s *Service) Login(ctx context.Context, email, password string) (string, *User, error) {
	var (
		id           string
		passwordHash string
		name         string
		role         string
		totpEnabled  bool
		totpSecret   string
		mustChange   bool
		tosAccepted  bool
		lastLogin    *time.Time
	)

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, password_hash, name, role, totp_enabled, totp_secret,
			must_change_password, tos_accepted, last_login
		FROM users WHERE email = $1`, email).Scan(
		&id, &passwordHash, &name, &role, &totpEnabled, &totpSecret,
		&mustChange, &tosAccepted, &lastLogin,
	)
	if err != nil {
		return "", nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
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
	}
	if lastLogin != nil {
		user.LastLogin = *lastLogin
	}

	token, err := s.generateToken(user)
	if err != nil {
		return "", nil, err
	}

	// Update last login
	_, _ = s.db.Pool.Exec(ctx, `UPDATE users SET last_login = NOW() WHERE id = $1`, id)

	return token, user, nil
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

// HashPassword hashes a password with bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
