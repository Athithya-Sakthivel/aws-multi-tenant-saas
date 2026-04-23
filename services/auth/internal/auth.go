package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const (
	tenantMigrationVersion = "00001_init"
	passwordHashCost       = 12
	tokenTTL               = time.Hour
)

var (
	ErrInvalidRequest     = errors.New("invalid request")
	ErrInvalidTenant      = errors.New("invalid tenant")
	ErrTenantNotFound     = errors.New("tenant not found")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrPasswordTooShort   = errors.New("password too short")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailAlreadyExists = errors.New("email already exists")
)

type Service struct {
	db                 *DB
	jwtSecret          []byte
	tenantMigrationSQL string
	tokenTTL           time.Duration
}

type TenantInput struct {
	Tenant string `json:"tenant"`
}

type RegisterInput struct {
	Tenant   string `json:"tenant"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginInput struct {
	Tenant   string `json:"tenant"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type Claims struct {
	Tenant string `json:"tenant"`
	jwt.RegisteredClaims
}

func NewService(db *DB, jwtSecret []byte, tenantMigrationSQL []byte) *Service {
	secretCopy := make([]byte, len(jwtSecret))
	copy(secretCopy, jwtSecret)

	migrationCopy := make([]byte, len(tenantMigrationSQL))
	copy(migrationCopy, tenantMigrationSQL)

	return &Service{
		db:                 db,
		jwtSecret:          secretCopy,
		tenantMigrationSQL: string(migrationCopy),
		tokenTTL:           tokenTTL,
	}
}

func (s *Service) Ready(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Service) EnsureTenant(ctx context.Context, rawTenant string) error {
	tenant, err := normalizeTenantName(rawTenant)
	if err != nil {
		return err
	}

	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.tenants (tenant) VALUES ($1) ON CONFLICT DO NOTHING`, tenant); err != nil {
			return fmt.Errorf("register tenant: %w", err)
		}
		return s.migrateTenantTx(ctx, tx, tenant)
	})
}

func (s *Service) Register(ctx context.Context, in RegisterInput) error {
	tenant, err := normalizeTenantName(in.Tenant)
	if err != nil {
		return err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return err
	}

	if err := validatePassword(in.Password); err != nil {
		return err
	}

	exists, err := s.db.TenantExists(ctx, tenant)
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	if !exists {
		return ErrTenantNotFound
	}

	if err := s.migrateTenant(ctx, tenant); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), passwordHashCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	return s.db.WithTenantTx(ctx, tenant, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO users (email, password_hash) VALUES ($1, $2)`,
			email, string(hash),
		)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrEmailAlreadyExists
			}
			return fmt.Errorf("insert user: %w", err)
		}
		return nil
	})
}

func (s *Service) Login(ctx context.Context, in LoginInput) (string, error) {
	tenant, err := normalizeTenantName(in.Tenant)
	if err != nil {
		return "", err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(in.Password) == "" {
		return "", ErrInvalidCredentials
	}

	exists, err := s.db.TenantExists(ctx, tenant)
	if err != nil {
		return "", fmt.Errorf("tenant lookup: %w", err)
	}
	if !exists {
		return "", ErrTenantNotFound
	}

	if err := s.migrateTenant(ctx, tenant); err != nil {
		return "", err
	}

	var userID int64
	var passwordHash string

	err = s.db.WithTenantTx(ctx, tenant, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT id, password_hash FROM users WHERE email = $1`,
			email,
		).Scan(&userID, &passwordHash)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("select user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(in.Password)); err != nil {
		return "", ErrInvalidCredentials
	}

	return s.issueToken(userID, tenant)
}

func (s *Service) migrateTenant(ctx context.Context, tenant string) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		return s.migrateTenantTx(ctx, tx, tenant)
	})
}

func (s *Service) migrateTenantTx(ctx context.Context, tx *sql.Tx, tenant string) error {
	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(tenant)); err != nil {
		return fmt.Errorf("create tenant schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path = `+quoteIdentifier(tenant)+`, public`); err != nil {
		return fmt.Errorf("set tenant search_path: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("ensure migration ledger: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, "auth:tenant:"+tenant); err != nil {
		return fmt.Errorf("lock tenant migration: %w", err)
	}

	var applied bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
		tenantMigrationVersion,
	).Scan(&applied); err != nil {
		return fmt.Errorf("check tenant migration: %w", err)
	}
	if applied {
		return nil
	}

	if _, err := tx.ExecContext(ctx, s.tenantMigrationSQL); err != nil {
		return fmt.Errorf("apply tenant migration: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1)`,
		tenantMigrationVersion,
	); err != nil {
		return fmt.Errorf("record tenant migration: %w", err)
	}

	return nil
}

func (s *Service) issueToken(userID int64, tenant string) (string, error) {
	now := time.Now().UTC()

	claims := Claims{
		Tenant: tenant,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "auth-service",
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

func normalizeTenantName(raw string) (string, error) {
	tenant := strings.ToLower(strings.TrimSpace(raw))
	if err := validateTenantName(tenant); err != nil {
		return "", err
	}
	return tenant, nil
}

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", ErrInvalidEmail
	}

	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", ErrInvalidEmail
	}

	normalized := strings.ToLower(strings.TrimSpace(addr.Address))
	if normalized == "" {
		return "", ErrInvalidEmail
	}

	return normalized, nil
}

func validatePassword(password string) error {
	if len(password) < 12 {
		return ErrPasswordTooShort
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}
