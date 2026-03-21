package sites

import (
	"context"
	"errors"
	"time"

	"github.com/karamble/diginode-cc/internal/database"
)

var ErrSiteNotFound = errors.New("site not found")

// Site represents a deployment location.
type Site struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Latitude    float64   `json:"latitude,omitempty"`
	Longitude   float64   `json:"longitude,omitempty"`
	RadiusM     float64   `json:"radiusM"`
	IsPrimary   bool      `json:"isPrimary"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Service manages multi-site configuration.
type Service struct {
	db *database.DB
}

// NewService creates a new site service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// GetAll returns all sites.
func (s *Service) GetAll(ctx context.Context) ([]*Site, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, description, latitude, longitude, radius_m, is_primary, created_at
		FROM sites ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []*Site
	for rows.Next() {
		var site Site
		if err := rows.Scan(&site.ID, &site.Name, &site.Description,
			&site.Latitude, &site.Longitude, &site.RadiusM,
			&site.IsPrimary, &site.CreatedAt); err != nil {
			continue
		}
		sites = append(sites, &site)
	}
	return sites, nil
}

// Create adds a new site.
func (s *Service) Create(ctx context.Context, site *Site) error {
	return s.db.Pool.QueryRow(ctx, `
		INSERT INTO sites (name, description, latitude, longitude, radius_m, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		site.Name, site.Description, site.Latitude, site.Longitude,
		site.RadiusM, site.IsPrimary,
	).Scan(&site.ID)
}

// GetByID returns a site by ID.
func (s *Service) GetByID(ctx context.Context, id string) (*Site, error) {
	var site Site
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, name, description, latitude, longitude, radius_m, is_primary, created_at
		FROM sites WHERE id = $1`, id).Scan(
		&site.ID, &site.Name, &site.Description,
		&site.Latitude, &site.Longitude, &site.RadiusM,
		&site.IsPrimary, &site.CreatedAt,
	)
	if err != nil {
		return nil, ErrSiteNotFound
	}
	return &site, nil
}

// Delete removes a site.
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM sites WHERE id = $1`, id)
	return err
}
