//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type client struct {
	baseURL    string
	adminToken string
	http       *http.Client
}

func newClient(t *testing.T) *client {
	t.Helper()

	base := os.Getenv("ZAPFAST_BASE_URL")
	if base == "" {
		t.Skip("ZAPFAST_BASE_URL not set; skipping e2e")
	}
	admin := os.Getenv("ZAPFAST_ADMIN_TOKEN")
	if admin == "" {
		t.Skip("ZAPFAST_ADMIN_TOKEN not set; skipping e2e")
	}

	tlsInsecure := os.Getenv("ZAPFAST_TLS_INSECURE") == "1"

	return &client{
		baseURL:    strings.TrimRight(base, "/"),
		adminToken: admin,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: tlsInsecure},
				DisableKeepAlives: true,
			},
		},
	}
}

func (c *client) do(t *testing.T, method, path string, headers map[string]string, body any) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (c *client) admin(t *testing.T, method, path string, body any) (int, []byte) {
	return c.do(t, method, path, map[string]string{"Authorization": c.adminToken}, body)
}

func (c *client) user(t *testing.T, token, method, path string, body any) (int, []byte) {
	return c.do(t, method, path, map[string]string{"token": token}, body)
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

func TestHealth(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	status, body := c.do(t, http.MethodGet, "/health", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", status, body)
	}

	var h struct {
		Status            string `json:"status"`
		Version           string `json:"version"`
		ActiveConnections int    `json:"active_connections"`
		Goroutines        int    `json:"goroutines"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if h.Status != "ok" {
		t.Errorf("status: want 'ok', got %q", h.Status)
	}
	if h.Version == "" {
		t.Errorf("version is empty")
	}
	if h.Goroutines <= 0 {
		t.Errorf("goroutines: want >0, got %d", h.Goroutines)
	}
}

func TestAdminAuth_Rejects(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	status, _ := c.do(t, http.MethodGet, "/admin/users", nil, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("no auth: want 401, got %d", status)
	}

	status, _ = c.do(t, http.MethodGet, "/admin/users",
		map[string]string{"Authorization": "wrong-" + randomHex(t, 8)}, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", status)
	}
}

func TestUserLifecycle(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	name := "e2e-" + randomHex(t, 6)
	userToken := randomHex(t, 16)

	status, body := c.admin(t, http.MethodPost, "/admin/users", map[string]string{
		"name":  name,
		"token": userToken,
	})
	if status != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", status, body)
	}
	var created struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !created.Success || created.Data.ID == "" {
		t.Fatalf("create response invalid: %s", body)
	}
	id := created.Data.ID

	t.Cleanup(func() {
		status, body := c.admin(t, http.MethodDelete, "/admin/users/"+id, nil)
		if status != http.StatusOK {
			t.Logf("cleanup delete failed: %d %s", status, body)
		}
	})

	status, body = c.user(t, userToken, http.MethodGet, "/session/status", nil)
	if status != http.StatusOK {
		t.Fatalf("session/status: want 200, got %d: %s", status, body)
	}
	var sess struct {
		Code int `json:"code"`
		Data struct {
			Connected bool   `json:"connected"`
			LoggedIn  bool   `json:"loggedIn"`
			ID        string `json:"id"`
			Name      string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if sess.Data.Connected {
		t.Errorf("new instance should not be connected")
	}
	if sess.Data.LoggedIn {
		t.Errorf("new instance should not be logged in")
	}
	if sess.Data.Name != name {
		t.Errorf("name: want %q, got %q", name, sess.Data.Name)
	}

	status, body = c.user(t, "wrong-token", http.MethodGet, "/session/status", nil)
	if status != http.StatusUnauthorized {
		t.Errorf("wrong user token: want 401, got %d: %s", status, body)
	}
}

func TestWebhookConfig(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	name := "e2e-webhook-" + randomHex(t, 6)
	userToken := randomHex(t, 16)

	status, body := c.admin(t, http.MethodPost, "/admin/users", map[string]string{
		"name":  name,
		"token": userToken,
	})
	if status != http.StatusCreated {
		t.Fatalf("create user: %d %s", status, body)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &created)

	t.Cleanup(func() {
		_, _ = c.admin(t, http.MethodDelete, "/admin/users/"+created.Data.ID, nil)
	})

	webhookURL := "https://example.com/zapfast-test-" + randomHex(t, 4)
	status, body = c.user(t, userToken, http.MethodPost, "/webhook", map[string]any{
		"webhookurl": webhookURL,
		"events":     []string{"Message", "ReadReceipt"},
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("set webhook: want 200/201, got %d: %s", status, body)
	}

	status, body = c.user(t, userToken, http.MethodGet, "/webhook", nil)
	if status != http.StatusOK {
		t.Fatalf("get webhook: %d %s", status, body)
	}
	var got struct {
		Data struct {
			Webhook   string   `json:"webhook"`
			Subscribe []string `json:"subscribe"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}
	if got.Data.Webhook != webhookURL {
		t.Errorf("webhook url: want %q, got %q", webhookURL, got.Data.Webhook)
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	status, _ := c.do(t, http.MethodGet, "/this-path-does-not-exist-"+randomHex(t, 4), nil, nil)
	if status != http.StatusNotFound && status != http.StatusUnauthorized {
		t.Errorf("unknown path: want 404 or 401, got %d", status)
	}
}

func TestResponseEnvelope(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	status, body := c.admin(t, http.MethodGet, "/admin/users", nil)
	if status != http.StatusOK {
		t.Fatalf("list users: %d %s", status, body)
	}
	var env struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != 200 {
		t.Errorf("envelope code: want 200, got %d", env.Code)
	}
	if !env.Success {
		t.Errorf("envelope success: want true")
	}
}

func TestConcurrentAdminReads(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	const n = 8
	done := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			s, _ := c.admin(t, http.MethodGet, "/admin/users", nil)
			done <- s
		}()
	}

	deadline := time.After(15 * time.Second)
	got := 0
	for got < n {
		select {
		case <-deadline:
			t.Fatalf("only %d/%d concurrent reads completed", got, n)
		case status := <-done:
			if status != http.StatusOK {
				t.Errorf("concurrent admin read: got %d", status)
			}
			got++
		}
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("ZAPFAST_BASE_URL") == "" {
		fmt.Fprintln(os.Stderr, "[e2e] ZAPFAST_BASE_URL not set — tests will be skipped per-test.")
	}
	os.Exit(m.Run())
}
