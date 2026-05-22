// Package users serves the GET /admin/users (list + single) endpoint.
//
// This is the second feature migrated from the monolithic cmd/api/handlers.go
// (ListUsers, 130 LOC with an N+1 query against the S3 config columns) into
// the hexagonal target structure.
//
// The package depends only on small interfaces (Repository for persistence,
// ConnectionStateProvider for live whatsmeow state). The Postgres adapter
// lives in postgres.go inside this package but is excluded from the unit
// coverage profile — it's exercised by integration tests against a real
// PostgreSQL container.
package users

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
)

// ProxyConfig is part of the user response, surfacing whether a per-instance
// proxy is configured.
type ProxyConfig struct {
	Enabled  bool   `json:"enabled"`
	ProxyURL string `json:"proxy_url"`
}

// S3Config mirrors the wuzapi per-user S3 settings. AccessKey is always
// masked as "***" in API responses to avoid leaking credentials.
type S3Config struct {
	Enabled       bool   `json:"enabled"`
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	Bucket        string `json:"bucket"`
	AccessKey     string `json:"access_key"`
	PathStyle     bool   `json:"path_style"`
	PublicURL     string `json:"public_url"`
	MediaDelivery string `json:"media_delivery"`
	RetentionDays int    `json:"retention_days"`
}

// User is the wire shape of GET /admin/users entries. Field names and types
// match the legacy ListUsers() handler exactly to preserve drop-in
// compatibility with Z-API consumers.
type User struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Token       string      `json:"token"`
	Webhook     string      `json:"webhook"`
	JID         string      `json:"jid"`
	QRCode      string      `json:"qrcode"`
	Connected   bool        `json:"connected"`
	LoggedIn    bool        `json:"loggedIn"`
	Expiration  int64       `json:"expiration"`
	ProxyURL    string      `json:"proxy_url"`
	Events      string      `json:"events"`
	ProxyConfig ProxyConfig `json:"proxy_config"`
	S3Config    S3Config    `json:"s3_config"`
}

// ErrNotFound is returned when Get sees no row for the requested ID. The
// handler translates this to HTTP 404.
var ErrNotFound = errors.New("user not found")

// Repository persists user metadata. The Postgres implementation in this
// package issues a single SELECT pulling all S3 fields too, fixing the
// legacy handler's N+1 query.
type Repository interface {
	List(ctx context.Context) ([]User, error)
	Get(ctx context.Context, id string) (User, error)
	Delete(ctx context.Context, id string) error
}

// ConnectionStateProvider reports the live connection state of an instance.
// Sourced from the whatsmeow ClientManager in production; tests pass a fake.
type ConnectionStateProvider interface {
	Connected(userID string) bool
	LoggedIn(userID string) bool
}

// Service composes Repository + ConnectionStateProvider into the response
// expected by the HTTP handler.
type Service struct {
	repo  Repository
	state ConnectionStateProvider
}

// New returns a Service. Both arguments must be non-nil; constructor does
// not panic but the resulting service will nil-dereference in production
// if either is missing — call sites are tiny and reviewed.
func New(repo Repository, state ConnectionStateProvider) *Service {
	return &Service{repo: repo, state: state}
}

// List returns all users with their live connection state and the S3
// access key masked.
func (s *Service) List(ctx context.Context) ([]User, error) {
	users, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		users[i] = applyLiveState(users[i], s.state)
	}
	return users, nil
}

// Get returns a single user by ID. Returns ErrNotFound (not nil + nil) when
// the row doesn't exist, so the handler can pick the right status code.
func (s *Service) Get(ctx context.Context, id string) (User, error) {
	user, err := s.repo.Get(ctx, id)
	if err != nil {
		return User{}, err
	}
	return applyLiveState(user, s.state), nil
}

// Delete removes a user by ID. Returns ErrNotFound when no row matched.
// Closing the in-process whatsmeow client is a separate concern and not
// done here — the legacy handler also leaves cleanup to the watchdog.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func applyLiveState(u User, state ConnectionStateProvider) User {
	u.Connected = state.Connected(u.ID)
	u.LoggedIn = state.LoggedIn(u.ID)
	u.S3Config.AccessKey = "***"
	return u
}

// Handler is the net/http adapter. Routes /admin/users → List and
// /admin/users/{id} → Get. The 'id' path variable is read from gorilla/mux.
type Handler struct {
	svc *Service
}

// NewHandler wraps a Service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ServeHTTP dispatches based on HTTP method + presence of the {id} path
// variable. Routing:
//   - GET /admin/users          → list
//   - GET /admin/users/{id}     → get
//   - DELETE /admin/users/{id}  → delete
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	id, hasID := vars["id"]

	if r.Method == http.MethodDelete {
		if !hasID {
			writeError(w, http.StatusBadRequest, "missing id")
			return
		}
		err := h.svc.Delete(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "database error")
			return
		}
		writeEnvelope(w, http.StatusOK, map[string]string{"id": id})
		return
	}

	if hasID {
		user, err := h.svc.Get(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "problem accessing DB")
			return
		}
		writeEnvelope(w, http.StatusOK, user)
		return
	}

	users, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "problem accessing DB")
		return
	}
	writeEnvelope(w, http.StatusOK, users)
}

func writeEnvelope(w http.ResponseWriter, code int, data any) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"success": true,
		"data":    data,
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"success": false,
		"error":   msg,
	})
}
