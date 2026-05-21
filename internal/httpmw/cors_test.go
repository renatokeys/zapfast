package httpmw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMW(cfg CORSConfig) http.Handler {
	mw := CORS(cfg)
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	}))
}

func TestCORS_DefaultConfig_HasGitHubPagesAndLocalhost(t *testing.T) {
	t.Parallel()
	cfg := DefaultCORS()
	want := []string{
		"https://renatokeys.github.io",
		"http://localhost:3000",
	}
	for _, w := range want {
		found := false
		for _, o := range cfg.AllowedOrigins {
			if o == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultCORS missing origin %q", w)
		}
	}
	if !cfg.AllowCredentials {
		t.Errorf("DefaultCORS should allow credentials")
	}
}

func TestCORS_AllowedOrigin_SetsHeader(t *testing.T) {
	t.Parallel()
	h := newMW(DefaultCORS())
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Origin", "https://renatokeys.github.io")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://renatokeys.github.io" {
		t.Errorf("Allow-Origin: want exact origin, got %q", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("Allow-Credentials should be true")
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("Vary header missing")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("downstream not called: status %d", rec.Code)
	}
}

func TestCORS_DisallowedOrigin_NoHeaderButRequestPasses(t *testing.T) {
	t.Parallel()
	h := newMW(DefaultCORS())
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin: want empty for disallowed, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("downstream not called: %d", rec.Code)
	}
}

func TestCORS_NoOriginHeader_PassesUntouched(t *testing.T) {
	t.Parallel()
	h := newMW(DefaultCORS())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("no Origin → no CORS header")
	}
}

func TestCORS_Preflight_AllowedOrigin_Returns204(t *testing.T) {
	t.Parallel()
	h := newMW(DefaultCORS())
	req := httptest.NewRequest(http.MethodOptions, "/admin/users", nil)
	req.Header.Set("Origin", "https://renatokeys.github.io")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status: want 204, got %d", rec.Code)
	}
	allowMethods := rec.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(allowMethods, "POST") {
		t.Errorf("Allow-Methods should contain POST, got %q", allowMethods)
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowHeaders, "Authorization") {
		t.Errorf("Allow-Headers should contain Authorization, got %q", allowHeaders)
	}
	if rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Errorf("Max-Age missing or wrong: %q", rec.Header().Get("Access-Control-Max-Age"))
	}
}

func TestCORS_Wildcard_AllowsAnyOrigin(t *testing.T) {
	t.Parallel()
	cfg := CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{"Content-Type"},
	}
	h := newMW(cfg)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://anywhere.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("wildcard should yield *")
	}
}

func TestCORS_MaxAgeZero_NotEmitted(t *testing.T) {
	t.Parallel()
	cfg := DefaultCORS()
	cfg.MaxAge = 0
	h := newMW(cfg)
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://renatokeys.github.io")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Max-Age") != "" {
		t.Errorf("Max-Age should be empty when MaxAge=0")
	}
}

func TestStrIntoa(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{600, "600"},
		{1234567, "1234567"},
	}
	for _, c := range cases {
		if got := strIntoa(c.in); got != c.want {
			t.Errorf("strIntoa(%d) = %q; want %q", c.in, got, c.want)
		}
	}
}
