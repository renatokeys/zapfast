package users

import (
	"context"
	"database/sql"
	"errors"
)

// PostgresRepository is the production Repository backed by PostgreSQL.
//
// One SELECT pulls ALL fields (including S3) — fixes the legacy ListUsers
// N+1 query that issued one extra SELECT per row for S3 config.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository takes a *sql.DB pre-configured with the application
// connection pool. The repository itself doesn't tune pool sizing.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

const selectColumns = `
SELECT
  id,
  name,
  token,
  COALESCE(webhook, '') AS webhook,
  COALESCE(jid, '') AS jid,
  COALESCE(qrcode, '') AS qrcode,
  COALESCE(expiration, 0) AS expiration,
  COALESCE(proxy_url, '') AS proxy_url,
  COALESCE(events, '') AS events,
  COALESCE(s3_enabled, false) AS s3_enabled,
  COALESCE(s3_endpoint, '') AS s3_endpoint,
  COALESCE(s3_region, '') AS s3_region,
  COALESCE(s3_bucket, '') AS s3_bucket,
  COALESCE(s3_path_style, false) AS s3_path_style,
  COALESCE(s3_public_url, '') AS s3_public_url,
  COALESCE(media_delivery, '') AS media_delivery,
  COALESCE(s3_retention_days, 0) AS s3_retention_days
FROM users
`

// List returns all users via a single SELECT.
func (r *PostgresRepository) List(ctx context.Context) ([]User, error) {
	rows, err := r.db.QueryContext(ctx, selectColumns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Delete removes a user row by ID. Returns ErrNotFound when no row matched.
func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns a single user. ErrNotFound is returned for missing rows so
// callers can map to HTTP 404.
func (r *PostgresRepository) Get(ctx context.Context, id string) (User, error) {
	rows, err := r.db.QueryContext(ctx, selectColumns+" WHERE id = $1 LIMIT 1", id)
	if err != nil {
		return User{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return User{}, err
		}
		return User{}, ErrNotFound
	}
	return scanRow(rows)
}

// scanner is the subset of *sql.Rows needed by scanRow — kept as an
// interface so tests could inject a fake (currently unused; PostgresRepository
// is covered by integration tests against a real container).
type scanner interface {
	Scan(dest ...any) error
}

func scanRow(s scanner) (User, error) {
	var u User
	if err := s.Scan(
		&u.ID, &u.Name, &u.Token,
		&u.Webhook, &u.JID, &u.QRCode,
		&u.Expiration, &u.ProxyURL, &u.Events,
		&u.S3Config.Enabled, &u.S3Config.Endpoint, &u.S3Config.Region, &u.S3Config.Bucket,
		&u.S3Config.PathStyle, &u.S3Config.PublicURL, &u.S3Config.MediaDelivery, &u.S3Config.RetentionDays,
	); err != nil {
		return User{}, err
	}
	u.ProxyConfig = ProxyConfig{Enabled: u.ProxyURL != "", ProxyURL: u.ProxyURL}
	return u, nil
}

// ensure errors are referenced (compiler check)
var _ = errors.Is
