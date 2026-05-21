// Package health produces the payload served by GET /health.
//
// The package is the proof-of-concept of the hexagonal pattern that will be
// rolled out across the rest of zapfast as features migrate out of the
// monolithic cmd/api/handlers.go. The shape:
//
//   - Service depends only on small interfaces (Stats, Clock).
//   - Handler is a thin HTTP adapter wrapping Service.
//   - Production wiring lives in cmd/api/main.go; tests use fakes.
//
// 100% line coverage is required for everything under internal/.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

// Stats is the read-only view of runtime state the health endpoint needs.
// The production implementation reads from the *whatsmeow.Client map and the
// users table; tests pass a fake.
type Stats interface {
	TotalUsers(ctx context.Context) int
	ActiveConnections() int
	ConnectedUsers() int
	LoggedInUsers() int
}

// Clock allows tests to control the timestamp and uptime values.
type Clock interface {
	Now() time.Time
}

// SystemClock is the wall-clock implementation of Clock.
type SystemClock struct{}

// Now returns the current local time.
func (SystemClock) Now() time.Time { return time.Now() }

// MemReader exposes Go runtime memory stats. Indirected so tests can fake
// out runtime.ReadMemStats / runtime.NumGoroutine without touching globals.
type MemReader interface {
	Read() MemStats
	Goroutines() int
}

// MemStats is the subset of runtime.MemStats the health response exposes.
type MemStats struct {
	AllocMB      uint64
	TotalAllocMB uint64
	SysMB        uint64
	NumGC        uint32
}

// SystemMem reads stats from the Go runtime.
type SystemMem struct{}

// Read returns memory stats from runtime.ReadMemStats, converting to MB.
func (SystemMem) Read() MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return MemStats{
		AllocMB:      m.Alloc / 1024 / 1024,
		TotalAllocMB: m.TotalAlloc / 1024 / 1024,
		SysMB:        m.Sys / 1024 / 1024,
		NumGC:        m.NumGC,
	}
}

// Goroutines returns the current goroutine count.
func (SystemMem) Goroutines() int { return runtime.NumGoroutine() }

// Response is the JSON shape served at GET /health. Field names and casing
// must match the legacy wuzapi handler for drop-in API stability.
type Response struct {
	Status            string                 `json:"status"`
	Timestamp         string                 `json:"timestamp"`
	Uptime            string                 `json:"uptime"`
	ActiveConnections int                    `json:"active_connections"`
	TotalUsers        int                    `json:"total_users"`
	ConnectedUsers    int                    `json:"connected_users"`
	LoggedInUsers     int                    `json:"logged_in_users"`
	MemoryStats       map[string]interface{} `json:"memory_stats"`
	GoRoutines        int                    `json:"goroutines"`
	Version           string                 `json:"version,omitempty"`
}

// Service composes Stats + Clock + MemReader into a Response.
type Service struct {
	stats   Stats
	clock   Clock
	mem     MemReader
	start   time.Time
	version string
}

// Option customises Service construction. SystemClock + SystemMem are used
// by default, so tests can override either.
type Option func(*Service)

// WithClock overrides the default SystemClock.
func WithClock(c Clock) Option { return func(s *Service) { s.clock = c } }

// WithMem overrides the default SystemMem.
func WithMem(m MemReader) Option { return func(s *Service) { s.mem = m } }

// New returns a Service. version is reported in the response; pass an empty
// string to omit it.
func New(stats Stats, version string, opts ...Option) *Service {
	svc := &Service{
		stats:   stats,
		clock:   SystemClock{},
		mem:     SystemMem{},
		version: version,
	}
	for _, opt := range opts {
		opt(svc)
	}
	svc.start = svc.clock.Now()
	return svc
}

// Report returns the current health snapshot.
func (s *Service) Report(ctx context.Context) Response {
	now := s.clock.Now()
	mem := s.mem.Read()

	return Response{
		Status:            "ok",
		Timestamp:         now.UTC().Format(time.RFC3339),
		Uptime:            now.Sub(s.start).String(),
		ActiveConnections: s.stats.ActiveConnections(),
		TotalUsers:        s.stats.TotalUsers(ctx),
		ConnectedUsers:    s.stats.ConnectedUsers(),
		LoggedInUsers:     s.stats.LoggedInUsers(),
		MemoryStats: map[string]interface{}{
			"alloc_mb":       mem.AllocMB,
			"total_alloc_mb": mem.TotalAllocMB,
			"sys_mb":         mem.SysMB,
			"num_gc":         mem.NumGC,
		},
		GoRoutines: s.mem.Goroutines(),
		Version:    s.version,
	}
}

// Handler is a net/http adapter over Service.
type Handler struct{ svc *Service }

// NewHandler wraps the given Service in an HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// ServeHTTP writes the report as JSON with status 200.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := h.svc.Report(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
