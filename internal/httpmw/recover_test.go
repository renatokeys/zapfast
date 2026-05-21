package httpmw

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
)

func TestRecover_NoPanic_PassesThrough(t *testing.T) {
	t.Parallel()
	mw := Recover(zerolog.Nop())
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`ok`))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Errorf("inner handler should have been called")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status: want %d, got %d", http.StatusTeapot, rec.Code)
	}
}

func TestRecover_StringPanic_Returns500JSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	mw := Recover(logger)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom string")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: want application/json, got %q", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body not JSON: %v\nraw=%s", err, body)
	}
	if out["code"].(float64) != 500 || out["error"] != "internal server error" {
		t.Errorf("body shape: %v", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte("panic recovered")) {
		t.Errorf("expected log message; got: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("boom string")) {
		t.Errorf("expected panic value in log; got: %s", buf.String())
	}
}

func TestRecover_ErrorPanic_LogsStack(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	mw := Recover(logger)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(errors.New("structured boom"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/y", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", rec.Code)
	}
	if !bytes.Contains(buf.Bytes(), []byte("stack")) {
		t.Errorf("expected stack field in log; got: %s", buf.String())
	}
}

func TestRecover_NilPointerPanic_HandledGracefully(t *testing.T) {
	t.Parallel()
	mw := Recover(zerolog.Nop())
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		var p *struct{ X int }
		_ = p.X
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil pointer panic should yield 500, got %d", rec.Code)
	}
}
