package permissions

import (
	"context"

	"github.com/karamble/diginode-cc/internal/database"
)

// Feature defines a granular permission flag.
type Feature string

const (
	MapView         Feature = "map.view"
	InventoryView   Feature = "inventory.view"
	InventoryManage Feature = "inventory.manage"
	TargetsView     Feature = "targets.view"
	TargetsManage   Feature = "targets.manage"
	CommandsSend    Feature = "commands.send"
	CommandsAudit   Feature = "commands.audit"
	ConfigManage    Feature = "config.manage"
	AlarmsManage    Feature = "alarms.manage"
	ExportsGenerate Feature = "exports.generate"
	UsersManage     Feature = "users.manage"
	SchedulerManage Feature = "scheduler.manage"
)

// AllFeatures is the complete list of available features.
var AllFeatures = []Feature{
	MapView, InventoryView, InventoryManage,
	TargetsView, TargetsManage,
	CommandsSend, CommandsAudit,
	ConfigManage, AlarmsManage,
	ExportsGenerate, UsersManage, SchedulerManage,
}

// RoleDefaults returns the default features for a given role.
func RoleDefaults(role string) []Feature {
	switch role {
	case "ADMIN":
		return AllFeatures
	case "OPERATOR":
		return []Feature{
			MapView, InventoryView, InventoryManage,
			TargetsView, TargetsManage,
			CommandsSend, CommandsAudit,
			AlarmsManage, ExportsGenerate,
		}
	case "ANALYST":
		return []Feature{
			MapView, InventoryView,
			TargetsView, CommandsAudit,
			ExportsGenerate,
		}
	case "VIEWER":
		return []Feature{MapView, InventoryView, TargetsView}
	default:
		return nil
	}
}

// Service manages user feature permissions.
type Service struct {
	db *database.DB
}

// NewService creates a new permissions service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// GetUserFeatures returns the granted features for a user.
// Falls back to role defaults if no explicit permissions exist.
func (s *Service) GetUserFeatures(ctx context.Context, userID, role string) ([]Feature, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT feature FROM user_permissions
		WHERE user_id = $1 AND granted = true`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var features []Feature
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			continue
		}
		features = append(features, Feature(f))
	}

	if len(features) == 0 {
		return RoleDefaults(role), nil
	}
	return features, nil
}

// SetUserFeatures replaces all permissions for a user.
func (s *Service) SetUserFeatures(ctx context.Context, userID string, features []Feature) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM user_permissions WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}

	for _, f := range features {
		_, err = tx.Exec(ctx, `
			INSERT INTO user_permissions (user_id, feature, granted)
			VALUES ($1, $2, true)`, userID, string(f))
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
