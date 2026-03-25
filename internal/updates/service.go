package updates

import (
	"log/slog"
	"os/exec"
	"strings"
)

// Service handles git-based self-updates.
type Service struct {
	repoPath string
	remote   string
	branch   string
}

// NewService creates a new update service.
func NewService(repoPath, remote, branch string) *Service {
	return &Service{repoPath: repoPath, remote: remote, branch: branch}
}

func (s *Service) ref() string {
	return s.remote + "/" + s.branch
}

// CheckUpdate checks if a newer version is available.
func (s *Service) CheckUpdate() (bool, string, error) {
	// Fetch latest
	cmd := exec.Command("git", "-C", s.repoPath, "fetch", "--quiet")
	if err := cmd.Run(); err != nil {
		return false, "", err
	}

	// Compare HEAD with remote/branch
	cmd = exec.Command("git", "-C", s.repoPath, "rev-list", "--count", "HEAD.."+s.ref())
	out, err := cmd.Output()
	if err != nil {
		return false, "", err
	}

	count := strings.TrimSpace(string(out))
	if count == "0" {
		return false, "", nil
	}

	// Get latest commit message
	cmd = exec.Command("git", "-C", s.repoPath, "log", "--oneline", "-1", s.ref())
	msg, _ := cmd.Output()

	slog.Info("update available", "commits_behind", count)
	return true, strings.TrimSpace(string(msg)), nil
}

// ApplyUpdate pulls the latest changes and restarts.
func (s *Service) ApplyUpdate() error {
	cmd := exec.Command("git", "-C", s.repoPath, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("update failed", "output", string(out), "error", err)
		return err
	}

	slog.Info("update applied", "output", string(out))
	return nil
}

// CurrentVersion returns the current git version.
func (s *Service) CurrentVersion() string {
	cmd := exec.Command("git", "-C", s.repoPath, "describe", "--tags", "--always", "--dirty")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
