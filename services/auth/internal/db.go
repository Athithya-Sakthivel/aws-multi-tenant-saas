package internal

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type DB struct {
	*sql.DB
}

var tenantNameRegexp = regexp.MustCompile(`^[a-z0-9_]+$`)

func NewDB(ctx context.Context, dsn string) (*DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return &DB{DB: db}, nil
}

func (db *DB) Close() error {
	if db == nil || db.DB == nil {
		return nil
	}
	return db.DB.Close()
}

func (db *DB) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (db *DB) WithTenantTx(ctx context.Context, tenant string, fn func(*sql.Tx) error) error {
	if err := validateTenantName(tenant); err != nil {
		return err
	}

	return db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET LOCAL search_path = `+quoteIdentifier(tenant)+`, public`); err != nil {
			return fmt.Errorf("set tenant search_path: %w", err)
		}
		return fn(tx)
	})
}

func (db *DB) TenantExists(ctx context.Context, tenant string) (bool, error) {
	if err := validateTenantName(tenant); err != nil {
		return false, err
	}

	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM public.tenants
			WHERE tenant = $1
		)
	`, tenant).Scan(&exists); err != nil {
		return false, err
	}

	return exists, nil
}

func validateTenantName(tenant string) error {
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	if tenant == "" {
		return ErrInvalidTenant
	}
	if len(tenant) > 63 {
		return ErrInvalidTenant
	}
	if !tenantNameRegexp.MatchString(tenant) {
		return ErrInvalidTenant
	}
	switch tenant {
	case "public", "pg_catalog", "information_schema", "pg_toast":
		return ErrInvalidTenant
	}
	return nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
