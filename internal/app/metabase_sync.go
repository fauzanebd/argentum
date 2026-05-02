package app

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metabase"
)

// MetabaseWarehouseSync mirrors each tenant Postgres analytical DSN into a
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

// SyncCompanyPostgres upserts metabase/database for one connection row.
func (s *MetabaseWarehouseSync) SyncCompanyPostgres(ctx context.Context, conn *domain.DBConnection, dsnPlain string) (int, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("metabase client not configured")
	}
	if conn.DBType != db.Postgres {
		return 0, fmt.Errorf("metabase sync only supports postgres (%s)", conn.DBType)
	}
	details, err := metabase.PostgresMetabaseDetails(dsnPlain)
	if err != nil {
		return 0, err
	}
	name := s.syncName(conn)
	return s.client.UpsertPostgresWarehouse(ctx, conn.MetabaseDatabaseID, name, details)
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
