package users

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

type fakeRepo struct {
	listFn   func(context.Context) ([]User, error)
	getFn    func(context.Context, string) (User, error)
	deleteFn func(context.Context, string) error
}

func (f fakeRepo) List(ctx context.Context) ([]User, error) {
	return f.listFn(ctx)
}
func (f fakeRepo) Get(ctx context.Context, id string) (User, error) {
	return f.getFn(ctx, id)
}
func (f fakeRepo) Delete(ctx context.Context, id string) error {
	if f.deleteFn == nil {
		return nil
	}
	return f.deleteFn(ctx, id)
}

type fakeState struct {
	conn   map[string]bool
	logged map[string]bool
	calls  int
}

func (f *fakeState) Connected(id string) bool {
	f.calls++
	return f.conn[id]
}
func (f *fakeState) LoggedIn(id string) bool {
	f.calls++
	return f.logged[id]
}

func sample(id, name string) User {
	return User{
		ID: id, Name: name, Token: "t-" + id,
		Webhook: "https://hook/" + id, JID: id + "@s.whatsapp.net",
		Events: "All",
		S3Config: S3Config{
			Enabled: true, Bucket: "buck", AccessKey: "PLAINTEXT_SECRET",
		},
	}
}

func TestNew_StoresDependencies(t *testing.T) {
	t.Parallel()
	r := fakeRepo{}
	s := &fakeState{}
	svc := New(r, s)
	if svc.repo == nil || svc.state == nil {
		t.Errorf("New should store both deps")
	}
}

func TestService_List_AppliesLiveStateAndMasksAccessKey(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{listFn: func(ctx context.Context) ([]User, error) {
		return []User{sample("a", "Alice"), sample("b", "Bob")}, nil
	}}
	state := &fakeState{
		conn:   map[string]bool{"a": true, "b": false},
		logged: map[string]bool{"a": true, "b": false},
	}
	svc := New(repo, state)

	users, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 users, got %d", len(users))
	}
	if !users[0].Connected || !users[0].LoggedIn {
		t.Errorf("alice should be connected+loggedIn")
	}
	if users[1].Connected || users[1].LoggedIn {
		t.Errorf("bob should not be connected")
	}
	for _, u := range users {
		if u.S3Config.AccessKey != "***" {
			t.Errorf("access_key not masked: %q", u.S3Config.AccessKey)
		}
	}
	if state.calls != 4 {
		t.Errorf("expected 4 state calls (2 connected + 2 loggedIn), got %d", state.calls)
	}
}

func TestService_List_RepoError_Propagates(t *testing.T) {
	t.Parallel()
	want := errors.New("db down")
	repo := fakeRepo{listFn: func(context.Context) ([]User, error) { return nil, want }}
	svc := New(repo, &fakeState{})
	_, err := svc.List(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestService_Get_FoundUser(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{getFn: func(_ context.Context, id string) (User, error) {
		return sample(id, "Alice"), nil
	}}
	state := &fakeState{conn: map[string]bool{"a": true}}
	svc := New(repo, state)

	u, err := svc.Get(context.Background(), "a")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.ID != "a" || u.Name != "Alice" {
		t.Errorf("unexpected: %+v", u)
	}
	if !u.Connected {
		t.Errorf("alice should be connected")
	}
	if u.S3Config.AccessKey != "***" {
		t.Errorf("access_key not masked")
	}
}

func TestService_Get_NotFound(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{getFn: func(context.Context, string) (User, error) { return User{}, ErrNotFound }}
	svc := New(repo, &fakeState{})
	_, err := svc.Get(context.Background(), "xx")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err: want ErrNotFound, got %v", err)
	}
}

func TestService_Get_OtherError(t *testing.T) {
	t.Parallel()
	want := errors.New("explosion")
	repo := fakeRepo{getFn: func(context.Context, string) (User, error) { return User{}, want }}
	svc := New(repo, &fakeState{})
	_, err := svc.Get(context.Background(), "xx")
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func newRouterHandler(svc *Service) http.Handler {
	r := mux.NewRouter()
	h := NewHandler(svc)
	r.Handle("/admin/users", h).Methods("GET")
	r.Handle("/admin/users/{id}", h).Methods("GET")
	r.Handle("/admin/users/{id}", h).Methods("DELETE")
	r.Handle("/admin/users", h).Methods("DELETE")
	return r
}

func TestHandler_List_200(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{listFn: func(context.Context) ([]User, error) {
		return []User{sample("a", "Alice")}, nil
	}}
	h := newRouterHandler(New(repo, &fakeState{conn: map[string]bool{"a": true}}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: %q", ct)
	}
	var env struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    []struct {
			ID        string `json:"id"`
			Connected bool   `json:"connected"`
			S3Config  struct {
				AccessKey string `json:"access_key"`
			} `json:"s3_config"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	if env.Code != 200 || !env.Success {
		t.Errorf("envelope: %+v", env)
	}
	if len(env.Data) != 1 || env.Data[0].ID != "a" || !env.Data[0].Connected {
		t.Errorf("data: %+v", env.Data)
	}
	if env.Data[0].S3Config.AccessKey != "***" {
		t.Errorf("access_key not masked")
	}
}

func TestHandler_List_RepoError_500(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{listFn: func(context.Context) ([]User, error) { return nil, errors.New("boom") }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", rec.Code)
	}
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env["success"] != false {
		t.Errorf("envelope success should be false: %+v", env)
	}
}

func TestHandler_Get_200(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{getFn: func(_ context.Context, id string) (User, error) {
		return sample(id, "Alice"), nil
	}}
	h := newRouterHandler(New(repo, &fakeState{logged: map[string]bool{"a": true}}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users/a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var env struct {
		Data User `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.ID != "a" || !env.Data.LoggedIn {
		t.Errorf("data: %+v", env.Data)
	}
}

func TestHandler_Get_NotFound_404(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{getFn: func(context.Context, string) (User, error) { return User{}, ErrNotFound }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", rec.Code)
	}
}

func TestHandler_Get_OtherError_500(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{getFn: func(context.Context, string) (User, error) { return User{}, errors.New("kapow") }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/users/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", rec.Code)
	}
}

func TestService_Delete_Success(t *testing.T) {
	t.Parallel()
	gotID := ""
	repo := fakeRepo{deleteFn: func(_ context.Context, id string) error { gotID = id; return nil }}
	svc := New(repo, &fakeState{})
	if err := svc.Delete(context.Background(), "abc"); err != nil {
		t.Errorf("err: %v", err)
	}
	if gotID != "abc" {
		t.Errorf("id passed: %q", gotID)
	}
}

func TestService_Delete_Error(t *testing.T) {
	t.Parallel()
	want := errors.New("kaboom")
	repo := fakeRepo{deleteFn: func(context.Context, string) error { return want }}
	svc := New(repo, &fakeState{})
	if err := svc.Delete(context.Background(), "x"); !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestHandler_Delete_200(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{deleteFn: func(context.Context, string) error { return nil }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/admin/users/abc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code    int             `json:"code"`
		Success bool            `json:"success"`
		Data    map[string]string `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Success || env.Data["id"] != "abc" {
		t.Errorf("envelope: %+v", env)
	}
}

func TestHandler_Delete_NotFound_404(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{deleteFn: func(context.Context, string) error { return ErrNotFound }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/admin/users/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestHandler_Delete_OtherError_500(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{deleteFn: func(context.Context, string) error { return errors.New("db") }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/admin/users/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestHandler_Delete_MissingID_400(t *testing.T) {
	t.Parallel()
	// Direct serve without router so {id} isn't injected
	h := NewHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/admin/users", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}
