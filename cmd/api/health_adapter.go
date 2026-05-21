package main

import (
	"context"
	"database/sql"

	"github.com/renatokeys/zapfast/internal/health"
)

type healthStats struct {
	db *sql.DB
	cm *ClientManager
}

func (h healthStats) TotalUsers(ctx context.Context) int {
	if h.db == nil {
		return 0
	}
	var n int
	row := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users")
	if err := row.Scan(&n); err != nil {
		return 0
	}
	return n
}

func (h healthStats) ActiveConnections() int {
	if h.cm == nil {
		return 0
	}
	h.cm.RLock()
	defer h.cm.RUnlock()
	return len(h.cm.whatsmeowClients)
}

func (h healthStats) ConnectedUsers() int {
	if h.cm == nil {
		return 0
	}
	h.cm.RLock()
	defer h.cm.RUnlock()
	n := 0
	for _, c := range h.cm.whatsmeowClients {
		if c != nil && c.IsConnected() {
			n++
		}
	}
	return n
}

func (h healthStats) LoggedInUsers() int {
	if h.cm == nil {
		return 0
	}
	h.cm.RLock()
	defer h.cm.RUnlock()
	n := 0
	for _, c := range h.cm.whatsmeowClients {
		if c != nil && c.IsLoggedIn() {
			n++
		}
	}
	return n
}

func newHealthService(db *sql.DB, cm *ClientManager, ver string) *health.Service {
	return health.New(healthStats{db: db, cm: cm}, ver)
}
