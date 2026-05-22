package users

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
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

const insertSQL = `INSERT INTO users (
  id, name, token, webhook, expiration, events, jid, qrcode, proxy_url,
  s3_enabled, s3_endpoint, s3_region, s3_bucket, s3_access_key, s3_secret_key,
  s3_path_style, s3_public_url, media_delivery, s3_retention_days,
  hmac_key, history
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9,
  $10, $11, $12, $13, $14, $15,
  $16, $17, $18, $19,
  $20, $21
)`

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

// TokenExists returns true when at least one users row has the given token.
// Used by Service.Create to map duplicates to HTTP 409.
func (r *PostgresRepository) TokenExists(ctx context.Context, token string) (bool, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE token = $1", token).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ExistsByID returns true when at least one users row matches id.
func (r *PostgresRepository) ExistsByID(ctx context.Context, id string) (bool, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id = $1", id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetToken returns just the token column for the given user — used by
// Service.Update to compute oldToken before the patch is applied.
func (r *PostgresRepository) GetToken(ctx context.Context, id string) (string, error) {
	var t string
	if err := r.db.QueryRowContext(ctx, "SELECT token FROM users WHERE id = $1", id).Scan(&t); err != nil {
		return "", err
	}
	return t, nil
}

// Update applies the given fields to the user row in column order. The
// caller (Service.Update) is responsible for ensuring fields is non-empty.
func (r *PostgresRepository) Update(ctx context.Context, id string, fields []UpdateField) error {
	if len(fields) == 0 {
		return errors.New("no fields to update")
	}
	var b strings.Builder
	b.WriteString("UPDATE users SET ")
	args := make([]any, 0, len(fields)+1)
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Column)
		b.WriteString(" = $")
		b.WriteString(strconv.Itoa(i + 1))
		args = append(args, f.Value)
	}
	b.WriteString(" WHERE id = $")
	b.WriteString(strconv.Itoa(len(fields) + 1))
	args = append(args, id)
	_, err := r.db.ExecContext(ctx, b.String(), args...)
	return err
}

// Create writes a new user row with all proxy, S3 and HMAC columns.
func (r *PostgresRepository) Create(ctx context.Context, row CreateRow) error {
	_, err := r.db.ExecContext(ctx, insertSQL,
		row.ID, row.Name, row.Token, row.Webhook, row.Expiration, row.Events,
		"", "", row.ProxyURL,
		row.S3.Enabled, row.S3.Endpoint, row.S3.Region, row.S3.Bucket, row.S3.AccessKey, row.S3.SecretKey,
		row.S3.PathStyle, row.S3.PublicURL, row.S3.MediaDelivery, row.S3.RetentionDays,
		row.HMACKey, row.History,
	)
	return err
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

var _ = errors.Is
