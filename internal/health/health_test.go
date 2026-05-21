package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

type fakeStats struct {
	total, active, connected, loggedIn int
	ctxSeen                            context.Context
}

func (f *fakeStats) TotalUsers(ctx context.Context) int {
	f.ctxSeen = ctx
	return f.total
}
func (f *fakeStats) ActiveConnections() int { return f.active }
func (f *fakeStats) ConnectedUsers() int    { return f.connected }
func (f *fakeStats) LoggedInUsers() int     { return f.loggedIn }

type fakeClock struct {
	t time.Time
}

func (f *fakeClock) Now() time.Time { return f.t }

type fakeMem struct {
	stats      MemStats
	goroutines int
}

func (f fakeMem) Read() MemStats { return f.stats }
func (f fakeMem) Goroutines() int {
	return f.goroutines
}

func TestSystemClock_Now_ReturnsCurrentTime(t *testing.T) {
	t.Parallel()
	before := time.Now()
	got := SystemClock{}.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("SystemClock.Now() = %v; want in [%v, %v]", got, before, after)
	}
}

func TestSystemMem_Read_ReturnsRuntimeStats(t *testing.T) {
	t.Parallel()
	got := SystemMem{}.Read()
	// We can't assert exact values, but we can assert structure: at least
	// one of the fields must be non-zero in a running Go test process.
	if got.SysMB == 0 && got.AllocMB == 0 && got.TotalAllocMB == 0 {
		t.Errorf("SystemMem.Read() returned zero memory: %+v", got)
	}
}

func TestSystemMem_Goroutines_PositiveCount(t *testing.T) {
	t.Parallel()
	got := SystemMem{}.Goroutines()
	if got < 1 {
		t.Errorf("SystemMem.Goroutines() = %d; want >=1", got)
	}
	if got != runtime.NumGoroutine() && got+1 != runtime.NumGoroutine() {
		// goroutine count can vary by 1 between calls; allow tolerance
		t.Logf("informational: goroutines=%d runtime=%d", got, runtime.NumGoroutine())
	}
}

func TestNew_DefaultOptions(t *testing.T) {
	t.Parallel()
	stats := &fakeStats{}
	svc := New(stats, "v1.2.3")

	if svc.stats != stats {
		t.Errorf("stats not stored")
	}
	if svc.version != "v1.2.3" {
		t.Errorf("version: want v1.2.3, got %q", svc.version)
	}
	if _, ok := svc.clock.(SystemClock); !ok {
		t.Errorf("default clock should be SystemClock, got %T", svc.clock)
	}
	if _, ok := svc.mem.(SystemMem); !ok {
		t.Errorf("default mem should be SystemMem, got %T", svc.mem)
	}
	if svc.start.IsZero() {
		t.Errorf("start should be captured at New()")
	}
}

func TestNew_WithClock_OverridesDefault(t *testing.T) {
	t.Parallel()
	fc := &fakeClock{t: time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)}
	svc := New(&fakeStats{}, "", WithClock(fc))

	if svc.clock != fc {
		t.Errorf("WithClock should override default")
	}
	if !svc.start.Equal(fc.t) {
		t.Errorf("start: want %v, got %v", fc.t, svc.start)
	}
}

func TestNew_WithMem_OverridesDefault(t *testing.T) {
	t.Parallel()
	fm := fakeMem{stats: MemStats{AllocMB: 99}, goroutines: 42}
	svc := New(&fakeStats{}, "", WithMem(fm))

	if got := svc.mem.(fakeMem); got.stats.AllocMB != 99 {
		t.Errorf("WithMem should override; got AllocMB=%d", got.stats.AllocMB)
	}
}

func TestReport_AllFieldsPopulated(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
	later := start.Add(45 * time.Second)
	fc := &fakeClock{t: start}
	fm := fakeMem{
		stats:      MemStats{AllocMB: 3, TotalAllocMB: 10, SysMB: 15, NumGC: 7},
		goroutines: 23,
	}
	stats := &fakeStats{total: 100, active: 5, connected: 4, loggedIn: 3}

	svc := New(stats, "v0.1.0", WithClock(fc), WithMem(fm))
	fc.t = later

	resp := svc.Report(context.Background())

	if resp.Status != "ok" {
		t.Errorf("Status: want ok, got %q", resp.Status)
	}
	if resp.Timestamp != "2026-05-21T18:00:45Z" {
		t.Errorf("Timestamp: want 2026-05-21T18:00:45Z, got %q", resp.Timestamp)
	}
	if resp.Uptime != "45s" {
		t.Errorf("Uptime: want 45s, got %q", resp.Uptime)
	}
	if resp.ActiveConnections != 5 {
		t.Errorf("Active: want 5, got %d", resp.ActiveConnections)
	}
	if resp.TotalUsers != 100 {
		t.Errorf("TotalUsers: want 100, got %d", resp.TotalUsers)
	}
	if resp.ConnectedUsers != 4 {
		t.Errorf("ConnectedUsers: want 4, got %d", resp.ConnectedUsers)
	}
	if resp.LoggedInUsers != 3 {
		t.Errorf("LoggedInUsers: want 3, got %d", resp.LoggedInUsers)
	}
	if resp.GoRoutines != 23 {
		t.Errorf("GoRoutines: want 23, got %d", resp.GoRoutines)
	}
	if resp.Version != "v0.1.0" {
		t.Errorf("Version: want v0.1.0, got %q", resp.Version)
	}
	if got := resp.MemoryStats["alloc_mb"]; got != uint64(3) {
		t.Errorf("MemoryStats.alloc_mb: want 3, got %v", got)
	}
	if got := resp.MemoryStats["total_alloc_mb"]; got != uint64(10) {
		t.Errorf("MemoryStats.total_alloc_mb: want 10, got %v", got)
	}
	if got := resp.MemoryStats["sys_mb"]; got != uint64(15) {
		t.Errorf("MemoryStats.sys_mb: want 15, got %v", got)
	}
	if got := resp.MemoryStats["num_gc"]; got != uint32(7) {
		t.Errorf("MemoryStats.num_gc: want 7, got %v", got)
	}
}

func TestReport_OmitsVersionWhenEmpty(t *testing.T) {
	t.Parallel()
	fc := &fakeClock{t: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)}
	svc := New(&fakeStats{}, "", WithClock(fc), WithMem(fakeMem{goroutines: 1}))

	resp := svc.Report(context.Background())
	b, _ := json.Marshal(resp)
	if string(b) == "" {
		t.Fatalf("empty json")
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if _, present := out["version"]; present {
		t.Errorf("version field should be omitted when empty; got %v", out["version"])
	}
}

func TestReport_PropagatesContext(t *testing.T) {
	t.Parallel()
	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("token"), "abc")
	stats := &fakeStats{}
	svc := New(stats, "", WithClock(&fakeClock{t: time.Now()}), WithMem(fakeMem{}))

	_ = svc.Report(ctx)
	if stats.ctxSeen.Value(ctxKey("token")) != "abc" {
		t.Errorf("context not propagated to stats")
	}
}

func TestHandler_ServeHTTP_ReturnsJSON(t *testing.T) {
	t.Parallel()
	fc := &fakeClock{t: time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)}
	stats := &fakeStats{total: 42}
	svc := New(stats, "v1", WithClock(fc), WithMem(fakeMem{goroutines: 9}))

	h := NewHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Errorf("status: want 200, got %d", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: want application/json, got %q", ct)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, rec.Body.String())
	}
	if resp.Status != "ok" {
		t.Errorf("status field: want ok, got %q", resp.Status)
	}
	if resp.TotalUsers != 42 {
		t.Errorf("total_users: want 42, got %d", resp.TotalUsers)
	}
	if resp.Version != "v1" {
		t.Errorf("version: want v1, got %q", resp.Version)
	}
}
