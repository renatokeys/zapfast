// Package users serves the /admin/users CRUD endpoints (GET list, GET one,
// POST create, DELETE).
//
// The package depends only on small interfaces (Repository for persistence,
// ConnectionStateProvider for live whatsmeow state, IDGenerator and
// HMACEncryptor for side-effects the legacy code did inline). The Postgres
// adapter lives in postgres.go inside this package but is excluded from the
// unit coverage profile — it's exercised by integration tests against a real
// PostgreSQL container.
package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	SecretKey     string `json:"-"`
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

// CreateUserInput is the request shape for Service.Create.
type CreateUserInput struct {
	Name        string
	Token       string
	Webhook     string
	Expiration  int
	Events      string
	ProxyConfig ProxyConfig
	S3Config    S3Config
	HMACKey     string
	History     int
}

// CreateRow is the persisted row Repository.Create writes. HMACKey is the
// already-encrypted ciphertext (empty when the user supplied no HMAC).
type CreateRow struct {
	ID         string
	Name       string
	Token      string
	Webhook    string
	Expiration int
	Events     string
	ProxyURL   string
	S3         S3Config
	HMACKey    []byte
	History    int
}

// Sentinels mapped to HTTP responses by the handler.
var (
	ErrNotFound        = errors.New("user not found")
	ErrTokenExists     = errors.New("user with this token already exists")
	ErrInvalidEvent    = errors.New("invalid event type")
	ErrHMACKeyTooShort = errors.New("hmac key must be at least 32 characters long")
	ErrNoFields        = errors.New("no fields to update")
)

// UpdateInput is the patch shape for Service.Update. Zero values mean
// "do not change" — this matches legacy EditUser semantics so consumers
// can omit unchanged fields. ProxyConfig/S3Config use nil pointers to
// signal "not in patch" (otherwise Enabled=false would be indistinguishable
// from "absent").
type UpdateInput struct {
	Name        string
	Token       string
	Webhook     string
	Expiration  int
	Events      string
	History     int
	ProxyConfig *ProxyConfig
	S3Config    *S3Config
}

// UpdateResult is returned by Service.Update and passed to UpdateHook.
// OldToken/NewToken let the adapter invalidate the in-memory userinfo
// cache for both the previous and the new token.
type UpdateResult struct {
	UserID   string
	OldToken string
	NewToken string
	S3Config *S3Config
}

// UpdateHook is invoked after a successful Update. Adapters use it for
// side-effects (cache invalidation, S3 client init/remove) that the
// domain shouldn't know about.
type UpdateHook func(ctx context.Context, ev UpdateResult)

// Repository persists user metadata. The Postgres implementation in this
// package issues a single SELECT pulling all S3 fields too, fixing the
// legacy handler's N+1 query.
type Repository interface {
	List(ctx context.Context) ([]User, error)
	Get(ctx context.Context, id string) (User, error)
	Delete(ctx context.Context, id string) error
	TokenExists(ctx context.Context, token string) (bool, error)
	Create(ctx context.Context, row CreateRow) error
	ExistsByID(ctx context.Context, id string) (bool, error)
	GetToken(ctx context.Context, id string) (string, error)
	Update(ctx context.Context, id string, fields []UpdateField) error
}

// UpdateField is a single column-value pair used by Repository.Update.
// Driven by Service.Update which builds the slice in column order.
type UpdateField struct {
	Column string
	Value  any
}

// ConnectionStateProvider reports the live connection state of an instance.
// Sourced from the whatsmeow ClientManager in production; tests pass a fake.
type ConnectionStateProvider interface {
	Connected(userID string) bool
	LoggedIn(userID string) bool
}

// IDGenerator produces fresh user IDs. Defaults to crypto/rand 16 bytes hex.
type IDGenerator interface {
	Generate() (string, error)
}

// HMACEncryptor seals a plain HMAC key with the application encryption key.
// The default returns an error: callers that allow HMAC must inject one.
type HMACEncryptor interface {
	Encrypt(plain string) ([]byte, error)
}

type cryptoRandIDGen struct{ r io.Reader }

func (g cryptoRandIDGen) Generate() (string, error) {
	src := g.r
	if src == nil {
		src = rand.Reader
	}
	b := make([]byte, 16)
	if _, err := io.ReadFull(src, b); err != nil {
		return "", fmt.Errorf("failed to generate random ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

type disabledEncryptor struct{}

func (disabledEncryptor) Encrypt(_ string) ([]byte, error) {
	return nil, errors.New("hmac encryption not configured")
}

// Service composes Repository + ConnectionStateProvider + helpers into the
// response expected by the HTTP handler.
type Service struct {
	repo       Repository
	state      ConnectionStateProvider
	ids        IDGenerator
	crypto     HMACEncryptor
	updateHook UpdateHook
}

// Option mutates a Service at construction.
type Option func(*Service)

// WithIDGenerator overrides the default crypto/rand ID generator.
func WithIDGenerator(g IDGenerator) Option { return func(s *Service) { s.ids = g } }

// WithHMACEncryptor overrides the default (disabled) HMAC encryptor.
func WithHMACEncryptor(e HMACEncryptor) Option { return func(s *Service) { s.crypto = e } }

// WithUpdateHook registers a callback invoked after Service.Update. Used
// by the adapter to invalidate the userinfo cache and re-init S3 clients.
func WithUpdateHook(h UpdateHook) Option { return func(s *Service) { s.updateHook = h } }

// New returns a Service. Both arguments must be non-nil; constructor does
// not panic but the resulting service will nil-dereference in production
// if either is missing — call sites are tiny and reviewed.
func New(repo Repository, state ConnectionStateProvider, opts ...Option) *Service {
	s := &Service{repo: repo, state: state, ids: cryptoRandIDGen{}, crypto: disabledEncryptor{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// List returns all users with their live connection state and the S3
// access key masked.
func (s *Service) List(ctx context.Context) ([]User, error) {
	usersList, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range usersList {
		usersList[i] = applyLiveState(usersList[i], s.state)
	}
	return usersList, nil
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

// Create validates input, generates an ID, encrypts HMAC if provided,
// and persists the new user. Returns the created User (live state fields
// are zero — the instance isn't connected yet).
func (s *Service) Create(ctx context.Context, in CreateUserInput) (User, error) {
	if err := ValidateEvents(in.Events); err != nil {
		return User{}, err
	}
	var hmacBytes []byte
	if in.HMACKey != "" {
		if len(in.HMACKey) < 32 {
			return User{}, ErrHMACKeyTooShort
		}
		enc, err := s.crypto.Encrypt(in.HMACKey)
		if err != nil {
			return User{}, fmt.Errorf("encrypt hmac: %w", err)
		}
		hmacBytes = enc
	}
	exists, err := s.repo.TokenExists(ctx, in.Token)
	if err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrTokenExists
	}
	id, err := s.ids.Generate()
	if err != nil {
		return User{}, err
	}
	row := CreateRow{
		ID:         id,
		Name:       in.Name,
		Token:      in.Token,
		Webhook:    in.Webhook,
		Expiration: in.Expiration,
		Events:     in.Events,
		ProxyURL:   in.ProxyConfig.ProxyURL,
		S3:         in.S3Config,
		HMACKey:    hmacBytes,
		History:    in.History,
	}
	if err := s.repo.Create(ctx, row); err != nil {
		return User{}, err
	}
	created := User{
		ID:         id,
		Name:       in.Name,
		Token:      in.Token,
		Webhook:    in.Webhook,
		Expiration: int64(in.Expiration),
		Events:     in.Events,
		ProxyURL:   in.ProxyConfig.ProxyURL,
		ProxyConfig: ProxyConfig{
			Enabled:  in.ProxyConfig.ProxyURL != "",
			ProxyURL: in.ProxyConfig.ProxyURL,
		},
		S3Config: in.S3Config,
	}
	created.S3Config.AccessKey = "***"
	created.S3Config.SecretKey = ""
	return created, nil
}

// Update applies a partial patch to an existing user. Zero values on the
// string/int fields mean "don't change"; ProxyConfig/S3Config use nil
// pointers for the same purpose.
//
// Returns ErrNotFound if the user doesn't exist, ErrNoFields if the patch
// is empty, or ErrInvalidEvent for malformed Events.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (UpdateResult, error) {
	exists, err := s.repo.ExistsByID(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	if !exists {
		return UpdateResult{}, ErrNotFound
	}
	if in.Events != "" {
		if err := ValidateEvents(in.Events); err != nil {
			return UpdateResult{}, err
		}
	}
	oldToken, err := s.repo.GetToken(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	fields := buildUpdateFields(in)
	if len(fields) == 0 {
		return UpdateResult{}, ErrNoFields
	}
	if err := s.repo.Update(ctx, id, fields); err != nil {
		return UpdateResult{}, err
	}
	newToken := oldToken
	if in.Token != "" {
		newToken = in.Token
	}
	result := UpdateResult{
		UserID:   id,
		OldToken: oldToken,
		NewToken: newToken,
		S3Config: in.S3Config,
	}
	if s.updateHook != nil {
		s.updateHook(ctx, result)
	}
	return result, nil
}

func buildUpdateFields(in UpdateInput) []UpdateField {
	var f []UpdateField
	if in.Name != "" {
		f = append(f, UpdateField{Column: "name", Value: in.Name})
	}
	if in.Token != "" {
		f = append(f, UpdateField{Column: "token", Value: in.Token})
	}
	if in.Webhook != "" {
		f = append(f, UpdateField{Column: "webhook", Value: in.Webhook})
	}
	if in.Expiration != 0 {
		f = append(f, UpdateField{Column: "expiration", Value: in.Expiration})
	}
	if in.Events != "" {
		f = append(f, UpdateField{Column: "events", Value: in.Events})
	}
	if in.History != 0 {
		f = append(f, UpdateField{Column: "history", Value: in.History})
	}
	if in.ProxyConfig != nil {
		url := ""
		if in.ProxyConfig.Enabled {
			url = in.ProxyConfig.ProxyURL
		}
		f = append(f, UpdateField{Column: "proxy_url", Value: url})
	}
	if in.S3Config != nil {
		s3 := in.S3Config
		f = append(f,
			UpdateField{Column: "s3_enabled", Value: s3.Enabled},
			UpdateField{Column: "s3_endpoint", Value: s3.Endpoint},
			UpdateField{Column: "s3_region", Value: s3.Region},
			UpdateField{Column: "s3_bucket", Value: s3.Bucket},
			UpdateField{Column: "s3_access_key", Value: s3.AccessKey},
			UpdateField{Column: "s3_secret_key", Value: s3.SecretKey},
			UpdateField{Column: "s3_path_style", Value: s3.PathStyle},
			UpdateField{Column: "s3_public_url", Value: s3.PublicURL},
			UpdateField{Column: "media_delivery", Value: s3.MediaDelivery},
			UpdateField{Column: "s3_retention_days", Value: s3.RetentionDays},
		)
	}
	return f
}

func applyLiveState(u User, state ConnectionStateProvider) User {
	u.Connected = state.Connected(u.ID)
	u.LoggedIn = state.LoggedIn(u.ID)
	u.S3Config.AccessKey = "***"
	u.S3Config.SecretKey = ""
	return u
}

// Handler is the net/http adapter. Routes /admin/users → List/Create and
// /admin/users/{id} → Get/Delete. The 'id' path variable is read from
// gorilla/mux.
type Handler struct {
	svc *Service
}

// NewHandler wraps a Service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ServeHTTP dispatches based on HTTP method + presence of the {id} path
// variable. Routing:
//   - GET    /admin/users          → list
//   - POST   /admin/users          → create
//   - GET    /admin/users/{id}     → get
//   - PUT    /admin/users/{id}     → update
//   - DELETE /admin/users/{id}     → delete
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	id, hasID := vars["id"]

	switch r.Method {
	case http.MethodPost:
		h.handleCreate(w, r)
		return
	case http.MethodPut:
		h.handleUpdate(w, r, id, hasID)
		return
	case http.MethodDelete:
		h.handleDelete(w, r, id, hasID)
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

	usersList, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "problem accessing DB")
		return
	}
	writeEnvelope(w, http.StatusOK, usersList)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id string, hasID bool) {
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
}

// createPayload is the wire shape POST /admin/users accepts. It matches
// the legacy AddUser shape verbatim (camelCase for compatibility).
type createPayload struct {
	Name        string             `json:"name"`
	Token       string             `json:"token"`
	Webhook     string             `json:"webhook,omitempty"`
	Expiration  int                `json:"expiration,omitempty"`
	Events      string             `json:"events,omitempty"`
	ProxyConfig *createProxyConfig `json:"proxyConfig,omitempty"`
	S3Config    *createS3Config    `json:"s3Config,omitempty"`
	HMACKey     string             `json:"hmacKey,omitempty"`
	History     int                `json:"history,omitempty"`
}

type createProxyConfig struct {
	Enabled  bool   `json:"enabled"`
	ProxyURL string `json:"proxyURL"`
}

type createS3Config struct {
	Enabled       bool   `json:"enabled"`
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	Bucket        string `json:"bucket"`
	AccessKey     string `json:"access_key"`
	SecretKey     string `json:"secret_key"`
	PathStyle     bool   `json:"path_style"`
	PublicURL     string `json:"public_url"`
	MediaDelivery string `json:"media_delivery"`
	RetentionDays int    `json:"retention_days"`
}

// updatePayload is the wire shape for PUT /admin/users/{id}. Mirrors the
// legacy EditUser request shape (camelCase keys) verbatim.
type updatePayload struct {
	Name        string             `json:"name,omitempty"`
	Token       string             `json:"token,omitempty"`
	Webhook     string             `json:"webhook,omitempty"`
	Expiration  int                `json:"expiration,omitempty"`
	Events      string             `json:"events,omitempty"`
	History     int                `json:"history,omitempty"`
	ProxyConfig *createProxyConfig `json:"proxyConfig,omitempty"`
	S3Config    *createS3Config    `json:"s3Config,omitempty"`
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, id string, hasID bool) {
	if !hasID {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	var p updatePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}
	in := UpdateInput{
		Name:       p.Name,
		Token:      p.Token,
		Webhook:    p.Webhook,
		Expiration: p.Expiration,
		Events:     p.Events,
		History:    p.History,
	}
	if p.ProxyConfig != nil {
		in.ProxyConfig = &ProxyConfig{Enabled: p.ProxyConfig.Enabled, ProxyURL: p.ProxyConfig.ProxyURL}
	}
	if p.S3Config != nil {
		in.S3Config = &S3Config{
			Enabled:       p.S3Config.Enabled,
			Endpoint:      p.S3Config.Endpoint,
			Region:        p.S3Config.Region,
			Bucket:        p.S3Config.Bucket,
			AccessKey:     p.S3Config.AccessKey,
			SecretKey:     p.S3Config.SecretKey,
			PathStyle:     p.S3Config.PathStyle,
			PublicURL:     p.S3Config.PublicURL,
			MediaDelivery: p.S3Config.MediaDelivery,
			RetentionDays: p.S3Config.RetentionDays,
		}
	}
	_, err := h.svc.Update(r.Context(), id, in)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case errors.Is(err, ErrInvalidEvent):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, ErrNoFields):
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeEnvelopeWithMessage(w, http.StatusOK, "user updated successfully")
}

func writeEnvelopeWithMessage(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"success": true,
		"message": msg,
	})
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var p createPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}
	in := CreateUserInput{
		Name:       p.Name,
		Token:      p.Token,
		Webhook:    p.Webhook,
		Expiration: p.Expiration,
		Events:     p.Events,
		HMACKey:    p.HMACKey,
		History:    p.History,
	}
	if p.ProxyConfig != nil {
		in.ProxyConfig = ProxyConfig{Enabled: p.ProxyConfig.Enabled, ProxyURL: p.ProxyConfig.ProxyURL}
	}
	if p.S3Config != nil {
		in.S3Config = S3Config{
			Enabled:       p.S3Config.Enabled,
			Endpoint:      p.S3Config.Endpoint,
			Region:        p.S3Config.Region,
			Bucket:        p.S3Config.Bucket,
			AccessKey:     p.S3Config.AccessKey,
			SecretKey:     p.S3Config.SecretKey,
			PathStyle:     p.S3Config.PathStyle,
			PublicURL:     p.S3Config.PublicURL,
			MediaDelivery: p.S3Config.MediaDelivery,
			RetentionDays: p.S3Config.RetentionDays,
		}
	}
	user, err := h.svc.Create(r.Context(), in)
	switch {
	case errors.Is(err, ErrTokenExists):
		writeError(w, http.StatusConflict, "user with this token already exists")
		return
	case errors.Is(err, ErrInvalidEvent):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, ErrHMACKeyTooShort):
		writeError(w, http.StatusBadRequest, "HMAC key must be at least 32 characters long")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeEnvelope(w, http.StatusCreated, user)
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

// supportedEvents mirrors cmd/api/constants.go. Kept in-package so the
// hexagonal target has zero dependency on the legacy main package.
var supportedEvents = map[string]struct{}{
	"Message": {}, "UndecryptableMessage": {}, "Receipt": {}, "MediaRetry": {}, "ReadReceipt": {},
	"GroupInfo": {}, "JoinedGroup": {}, "Picture": {}, "BlocklistChange": {}, "Blocklist": {},
	"Connected": {}, "Disconnected": {}, "ConnectFailure": {}, "KeepAliveRestored": {}, "KeepAliveTimeout": {},
	"QRTimeout": {}, "LoggedOut": {}, "ClientOutdated": {}, "TemporaryBan": {}, "StreamError": {},
	"StreamReplaced": {}, "PairSuccess": {}, "PairError": {}, "QR": {}, "QRScannedWithoutMultidevice": {},
	"PrivacySettings": {}, "PushNameSetting": {}, "UserAbout": {},
	"AppState": {}, "AppStateSyncComplete": {}, "HistorySync": {}, "OfflineSyncCompleted": {}, "OfflineSyncPreview": {},
	"CallOffer": {}, "CallAccept": {}, "CallTerminate": {}, "CallOfferNotice": {}, "CallRelayLatency": {},
	"Presence": {}, "ChatPresence": {},
	"IdentityChange": {},
	"CATRefreshError": {},
	"NewsletterJoin": {}, "NewsletterLeave": {}, "NewsletterMuteChange": {}, "NewsletterLiveUpdate": {},
	"FBMessage": {},
	"All":       {},
}

// ValidateEvents accepts a comma-separated string of event types and
// returns ErrInvalidEvent wrapped with the offending token. An empty
// string is valid (subscribe to nothing).
func ValidateEvents(events string) error {
	if events == "" {
		return nil
	}
	for _, ev := range strings.Split(events, ",") {
		ev = strings.TrimSpace(ev)
		if ev == "" {
			continue
		}
		if _, ok := supportedEvents[ev]; !ok {
			return fmt.Errorf("%w: %s", ErrInvalidEvent, ev)
		}
	}
	return nil
}
