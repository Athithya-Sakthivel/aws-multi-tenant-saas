package internal

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

//go:embed migrations/*.sql
var tenantMigrationsFS embed.FS

const bootstrapSQL = `
CREATE TABLE IF NOT EXISTS public.schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.tenants (
	tenant TEXT PRIMARY KEY,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT tenants_name_lowercase_chk CHECK (tenant = lower(tenant))
);

INSERT INTO public.schema_migrations (version)
VALUES ('00001_bootstrap')
ON CONFLICT DO NOTHING;
`

type App struct {
	db  *DB
	svc *Service
	srv *http.Server
}

func NewApp(ctx context.Context) (*App, error) {
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return nil, err
	}

	db, err := NewDB(ctx, cfg.DSN)
	if err != nil {
		return nil, err
	}

	migrationSQL, err := tenantMigrationsFS.ReadFile("migrations/00001_init.sql")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read tenant migration: %w", err)
	}

	svc := NewService(db, cfg.JWTSecret, migrationSQL)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           NewHTTP(svc),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	return &App{
		db:  db,
		svc: svc,
		srv: srv,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	log.Printf("auth listening %s", a.srv.Addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := a.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("shutdown error: %v", err)
		}
	}()

	err = a.srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *App) Shutdown(ctx context.Context) error {
	var errs []error

	if err := a.srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, err)
	}
	if err := a.db.Close(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (a *App) Migrate(ctx context.Context) error {
	return a.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, "auth:bootstrap"); err != nil {
			return fmt.Errorf("lock bootstrap migration: %w", err)
		}

		if _, err := tx.ExecContext(ctx, bootstrapSQL); err != nil {
			return fmt.Errorf("apply bootstrap migration: %w", err)
		}

		return nil
	})
}
