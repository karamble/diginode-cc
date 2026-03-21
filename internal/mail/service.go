package mail

import (
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// Config holds SMTP configuration.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Service handles email delivery via SMTP.
type Service struct {
	cfg Config
}

// NewService creates a new mail service.
func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

// IsConfigured returns true if SMTP is configured.
func (s *Service) IsConfigured() bool {
	return s.cfg.Host != "" && s.cfg.From != ""
}

// Send sends an email.
func (s *Service) Send(to, subject, body string) error {
	if !s.IsConfigured() {
		slog.Warn("SMTP not configured, skipping email", "to", to, "subject", subject)
		return nil
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)

	msg := strings.Join([]string{
		"From: " + s.cfg.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	var auth smtp.Auth
	if s.cfg.User != "" {
		auth = smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)
	}

	err := smtp.SendMail(addr, auth, s.cfg.From, []string{to}, []byte(msg))
	if err != nil {
		slog.Error("failed to send email", "to", to, "error", err)
		return err
	}

	slog.Info("email sent", "to", to, "subject", subject)
	return nil
}

// SendPasswordReset sends a password reset email.
func (s *Service) SendPasswordReset(to, resetURL string) error {
	body := fmt.Sprintf(`
		<h2>DigiNode CC - Password Reset</h2>
		<p>Click the link below to reset your password:</p>
		<p><a href="%s">Reset Password</a></p>
		<p>This link expires in 1 hour.</p>
		<p>If you didn't request this, ignore this email.</p>
	`, resetURL)
	return s.Send(to, "Password Reset - DigiNode CC", body)
}

// SendPasswordResetAdmin sends a password reset email triggered by an admin.
func (s *Service) SendPasswordResetAdmin(to, resetURL, adminName string) error {
	body := fmt.Sprintf(`
		<h2>DigiNode CC - Password Reset</h2>
		<p>An administrator (%s) has requested a password reset for your account.</p>
		<p>Click the link below to set a new password:</p>
		<p><a href="%s">Reset Password</a></p>
		<p>This link expires in 1 hour.</p>
		<p>If you didn't expect this, contact your administrator.</p>
	`, adminName, resetURL)
	return s.Send(to, "Password Reset - DigiNode CC", body)
}

// SendInvitation sends a user invitation email.
func (s *Service) SendInvitation(to, inviteURL, invitedBy string) error {
	body := fmt.Sprintf(`
		<h2>DigiNode CC - Invitation</h2>
		<p>You've been invited by %s to join DigiNode CC.</p>
		<p><a href="%s">Accept Invitation</a></p>
		<p>This invitation expires in 7 days.</p>
	`, invitedBy, inviteURL)
	return s.Send(to, "Invitation - DigiNode CC", body)
}
