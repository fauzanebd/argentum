package app

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metabase"
)

// MetabaseWarehouseSync mirrors each tenant analytical DSN into a
// distinct Metabase "Database" (/api/database) so dashboards run on that tenant only.
type MetabaseWarehouseSync struct {
	client *metabase.Client
}

func NewMetabaseWarehouseSync(c *metabase.Client) *MetabaseWarehouseSync {
	if c == nil {
		return nil
	}
	return &MetabaseWarehouseSync{client: c}
}

func (s *MetabaseWarehouseSync) syncName(conn *domain.DBConnection) string {
	if conn.Label != "" {
		return fmt.Sprintf("%s [%s]", conn.Label, conn.ID)
	}
	return fmt.Sprintf("argentum-%s", conn.ID)
}

// SyncCompanyDatabase upserts metabase/database for one connection row.
func (s *MetabaseWarehouseSync) SyncCompanyDatabase(ctx context.Context, conn *domain.DBConnection, dsnPlain string) (int, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("metabase client not configured")
	}
	var details map[string]interface{}
	var engine string
	switch conn.DBType {
	case db.Postgres:
		engine = "postgres"
		var err error
		details, err = metabase.PostgresMetabaseDetails(dsnPlain)
		if err != nil {
			return 0, err
		}
	case db.MySQL:
		engine = "mysql"
		var err error
		details, err = metabase.MySQLMetabaseDetails(dsnPlain)
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("metabase sync does not support %s", conn.DBType)
	}
	name := s.syncName(conn)
	return s.client.UpsertWarehouse(ctx, engine, conn.MetabaseDatabaseID, name, details)
}

// DeleteWarehouse removes a tenant entry from Metabase (best-effort for cleanup).
func (s *MetabaseWarehouseSync) DeleteWarehouse(ctx context.Context, metabaseDBID int) {
	if s == nil || s.client == nil || metabaseDBID <= 0 {
		return
	}
	if err := s.client.DeleteWarehouse(ctx, metabaseDBID); err != nil {
		logrus.WithError(err).Warn("metabase: delete warehouse")
	}
}
