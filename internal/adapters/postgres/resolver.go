package postgres

import (
	"context"
	"fmt"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ConnectionResolver adapts ConnectionRepo to the db.ConnectionResolver
// contract: takes a company ID, returns the dialed db_type + plaintext DSN.
// Version is the row's updated_at as RFC3339Nano so the TenantConnPool can
// detect DSN rotations and re-dial.
type ConnectionResolver struct {
	repo *ConnectionRepo
	dsn  *crypto.DSNCipher
}

func NewConnectionResolver(repo *ConnectionRepo, dsn *crypto.DSNCipher) *ConnectionResolver {
	return &ConnectionResolver{repo: repo, dsn: dsn}
}

func (r *ConnectionResolver) Resolve(ctx context.Context, companyID string) (dbType, dsnPlain, version string, err error) {
	conn, err := r.repo.GetDefaultForCompany(ctx, companyID)
	if err != nil {
		if err == domain.ErrNotFound {
			return "", "", "", fmt.Errorf("company has no default DB connection")
		}
		return "", "", "", err
	}
	plain, err := r.dsn.Decrypt(conn.DSNEncrypted)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt DSN: %w", err)
	}
	return conn.DBType, plain, conn.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), nil
}
