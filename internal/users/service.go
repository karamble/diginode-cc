package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound     = errors.New("user not found")
	ErrEmailTaken       = errors.New("email already in use")
	ErrInvalidInvitation = errors.New("invalid or expired invitation")
)

// Role defines user permission levels.
type Role string

const (
	RoleAdmin    Role = "ADMIN"
	RoleOperator Role = "OPERATOR"
	RoleAnalyst  Role = "ANALYST"
	RoleViewer   Role = "VIEWER"
)

// User represents a system user.
type User struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name,omitempty"`
	Role               Role       `json:"role"`
	TOTPEnabled        bool       `json:"totpEnabled"`
	MustChangePassword bool       `json:"mustChangePassword"`
	TOSAccepted        bool       `json:"tosAccepted"`
	TOSAcceptedAt      *time.Time `json:"tosAcceptedAt,omitempty"`
	LastLogin          *time.Time `json:"lastLogin,omitempty"`
	SiteID             string     `json:"siteId,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// Invitation represents a user invitation.
type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      Role      `json:"role"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	InvitedBy string    `json:"invitedBy"`
}

// Service manages users and invitations.
type Service struct {
	db *database.DB
}

// NewService creates a new user service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// List returns all users.
func (s *Service) List(ctx context.Context) ([]*User, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, email, name, role, totp_enabled, must_change_password,
			tos_accepted, tos_accepted_at, last_login, site_id, created_at
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.TOTPEnabled,
			&u.MustChangePassword, &u.TOSAccepted, &u.TOSAcceptedAt,
			&u.LastLogin, &u.SiteID, &u.CreatedAt); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

// GetByID returns a user by ID.
func (s *Service) GetByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, email, name, role, totp_enabled, must_change_password,
			tos_accepted, tos_accepted_at, last_login, site_id, created_at
		FROM users WHERE id = $1`, id).Scan(
		&u.ID, &u.Email, &u.Name, &u.Role, &u.TOTPEnabled,
		&u.MustChangePassword, &u.TOSAccepted, &u.TOSAcceptedAt,
		&u.LastLogin, &u.SiteID, &u.CreatedAt,
	)
	if err != nil {
		return nil, ErrUserNotFound
	}
	return &u, nil
}

// Create creates a new user with a hashed password.
func (s *Service) Create(ctx context.Context, email, password, name string, role Role) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	var id string
	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, email, string(hash), name, string(role)).Scan(&id)
	if err != nil {
		return nil, ErrEmailTaken
	}

	return &User{
		ID:    id,
		Email: email,
		Name:  name,
		Role:  role,
	}, nil
}

// Update modifies a user's profile.
func (s *Service) Update(ctx context.Context, id string, name string, role Role) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE users SET name = $2, role = $3, updated_at = NOW()
		WHERE id = $1`, id, name, string(role))
	return err
}

// Delete removes a user.
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// CreateInvitation generates a user invitation.
func (s *Service) CreateInvitation(ctx context.Context, email string, role Role, invitedBy string) (*Invitation, error) {
	token, err := generateToken()
	if err != nil {
		return nil, err
	}

	inv := &Invitation{
		Email:     email,
		Role:      role,
		Token:     token,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		InvitedBy: invitedBy,
	}

	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO invitations (email, role, token, expires_at, invited_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		inv.Email, string(inv.Role), inv.Token, inv.ExpiresAt, inv.InvitedBy,
	).Scan(&inv.ID)
	if err != nil {
		return nil, err
	}

	return inv, nil
}

// AcceptTOS marks the Terms of Service as accepted.
func (s *Service) AcceptTOS(ctx context.Context, userID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE users SET tos_accepted = true, tos_accepted_at = NOW()
		WHERE id = $1`, userID)
	return err
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
