package users

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

var allColumns = []string{
	"id", "name", "token",
	"webhook", "jid", "qrcode",
	"expiration", "proxy_url", "events",
	"s3_enabled", "s3_endpoint", "s3_region", "s3_bucket",
	"s3_path_style", "s3_public_url", "media_delivery", "s3_retention_days",
}

func addSampleRow(rows *sqlmock.Rows, id, name string) *sqlmock.Rows {
	return rows.AddRow(
		id, name, "tok-"+id,
		"https://hook/"+id, id+"@s.whatsapp.net", "",
		int64(0), "socks5://example:1080", "Message,ReadReceipt",
		true, "https://s3.example.com", "us-east-1", "buck-"+id,
		false, "https://cdn.example/"+id, "base64", 30,
	)
}

func TestNewPostgresRepository_NotNil(t *testing.T) {
	t.Parallel()
	db, _ := newMockDB(t)
	if r := NewPostgresRepository(db); r == nil || r.db != db {
		t.Errorf("constructor")
	}
}

func TestPostgresRepository_List_TwoRows(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	rows := sqlmock.NewRows(allColumns)
	addSampleRow(rows, "a", "Alice")
	addSampleRow(rows, "b", "Bob")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT")).WillReturnRows(rows)

	r := NewPostgresRepository(db)
	users, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2, got %d", len(users))
	}
	if users[0].ID != "a" || users[0].Name != "Alice" {
		t.Errorf("row 0 wrong: %+v", users[0])
	}
	if users[0].Webhook != "https://hook/a" {
		t.Errorf("webhook: %q", users[0].Webhook)
	}
	if !users[0].S3Config.Enabled || users[0].S3Config.Bucket != "buck-a" {
		t.Errorf("s3: %+v", users[0].S3Config)
	}
	if !users[0].ProxyConfig.Enabled {
		t.Errorf("proxy_config.Enabled should be true (proxy_url not empty)")
	}
}

func TestPostgresRepository_List_QueryError(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	want := errors.New("connection refused")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT")).WillReturnError(want)

	r := NewPostgresRepository(db)
	_, err := r.List(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestPostgresRepository_List_ScanError(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	// Only 3 columns — Scan will fail mismatch
	rows := sqlmock.NewRows([]string{"id", "name", "token"}).AddRow("a", "Alice", "tok")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT")).WillReturnRows(rows)

	r := NewPostgresRepository(db)
	_, err := r.List(context.Background())
	if err == nil {
		t.Errorf("expected scan error")
	}
}

func TestPostgresRepository_List_RowsErr(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	want := errors.New("rows iter")
	rows := sqlmock.NewRows(allColumns).RowError(0, want)
	addSampleRow(rows, "a", "Alice")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT")).WillReturnRows(rows)

	r := NewPostgresRepository(db)
	_, err := r.List(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("rows.Err propagation: want %v, got %v", want, err)
	}
}

func TestPostgresRepository_Get_Found(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	rows := sqlmock.NewRows(allColumns)
	addSampleRow(rows, "x", "Xavier")
	mock.ExpectQuery(`WHERE id = \$1`).WithArgs("x").WillReturnRows(rows)

	r := NewPostgresRepository(db)
	u, err := r.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.ID != "x" || u.Name != "Xavier" {
		t.Errorf("wrong: %+v", u)
	}
}

func TestPostgresRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	rows := sqlmock.NewRows(allColumns) // empty
	mock.ExpectQuery(`WHERE id = \$1`).WithArgs("missing").WillReturnRows(rows)

	r := NewPostgresRepository(db)
	_, err := r.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err: want ErrNotFound, got %v", err)
	}
}

func TestPostgresRepository_Get_EmptyRowsErr(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	want := errors.New("close error")
	rows := sqlmock.NewRows(allColumns).CloseError(want) // no rows, error on close/iter
	mock.ExpectQuery(`WHERE id = \$1`).WithArgs("x").WillReturnRows(rows)

	r := NewPostgresRepository(db)
	_, err := r.Get(context.Background(), "x")
	// either the close error or ErrNotFound is acceptable; verify we get one
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestPostgresRepository_Get_QueryError(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	want := errors.New("net split")
	mock.ExpectQuery(`WHERE id = \$1`).WithArgs("a").WillReturnError(want)

	r := NewPostgresRepository(db)
	_, err := r.Get(context.Background(), "a")
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestPostgresRepository_Get_ScanError(t *testing.T) {
	t.Parallel()
	db, mock := newMockDB(t)
	rows := sqlmock.NewRows([]string{"id"}).AddRow("a")
	mock.ExpectQuery(`WHERE id = \$1`).WithArgs("a").WillReturnRows(rows)

	r := NewPostgresRepository(db)
	_, err := r.Get(context.Background(), "a")
	if err == nil {
		t.Errorf("expected scan error")
	}
}

type errScanner struct{ err error }

func (e errScanner) Scan(_ ...any) error { return e.err }

func TestScanRow_PropagatesScanError(t *testing.T) {
	t.Parallel()
	want := errors.New("scan boom")
	_, err := scanRow(errScanner{err: want})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

type stubScanner struct{}

func (s stubScanner) Scan(dest ...any) error {
	// Mimic what sql.Rows.Scan would do on a populated row — we just
	// leave defaults (zero values) so we can verify the post-scan
	// derivation logic (proxy_config.Enabled from proxy_url).
	if len(dest) < 17 {
		return errors.New("too few dests")
	}
	// Set proxy_url to empty string to assert ProxyConfig.Enabled = false
	if p, ok := dest[7].(*string); ok {
		*p = ""
	}
	return nil
}

func TestScanRow_ProxyConfigDisabledWhenURLEmpty(t *testing.T) {
	t.Parallel()
	u, err := scanRow(stubScanner{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.ProxyConfig.Enabled {
		t.Errorf("ProxyConfig.Enabled should be false when proxy_url empty")
	}
}
