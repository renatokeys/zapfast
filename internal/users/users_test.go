package users

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

type fakeRepo struct {
	listFn        func(context.Context) ([]User, error)
	getFn         func(context.Context, string) (User, error)
	deleteFn      func(context.Context, string) error
	tokenExistsFn func(context.Context, string) (bool, error)
	createFn      func(context.Context, CreateRow) error
	existsByIDFn  func(context.Context, string) (bool, error)
	getTokenFn    func(context.Context, string) (string, error)
	updateFn      func(context.Context, string, []UpdateField) error
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
func (f fakeRepo) TokenExists(ctx context.Context, token string) (bool, error) {
	if f.tokenExistsFn == nil {
		return false, nil
	}
	return f.tokenExistsFn(ctx, token)
}
func (f fakeRepo) Create(ctx context.Context, row CreateRow) error {
	if f.createFn == nil {
		return nil
	}
	return f.createFn(ctx, row)
}
func (f fakeRepo) ExistsByID(ctx context.Context, id string) (bool, error) {
	if f.existsByIDFn == nil {
		return true, nil
	}
	return f.existsByIDFn(ctx, id)
}
func (f fakeRepo) GetToken(ctx context.Context, id string) (string, error) {
	if f.getTokenFn == nil {
		return "", nil
	}
	return f.getTokenFn(ctx, id)
}
func (f fakeRepo) Update(ctx context.Context, id string, fields []UpdateField) error {
	if f.updateFn == nil {
		return nil
	}
	return f.updateFn(ctx, id, fields)
}

type stubIDGen struct {
	id  string
	err error
}

func (s stubIDGen) Generate() (string, error) { return s.id, s.err }

type stubEncryptor struct {
	out []byte
	err error
}

func (s stubEncryptor) Encrypt(_ string) ([]byte, error) { return s.out, s.err }

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
	r.Handle("/admin/users", h).Methods("POST")
	r.Handle("/admin/users/{id}", h).Methods("PUT")
	r.Handle("/admin/users", h).Methods("PUT")
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

func TestValidateEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantErr error
	}{
		{"", nil},
		{"Message", nil},
		{"Message,ReadReceipt,All", nil},
		{"Message, , ReadReceipt", nil},
		{"Bogus", ErrInvalidEvent},
		{"Message,Bogus", ErrInvalidEvent},
	}
	for _, c := range cases {
		err := ValidateEvents(c.in)
		if c.wantErr == nil && err != nil {
			t.Errorf("%q: unexpected err %v", c.in, err)
		}
		if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Errorf("%q: want %v, got %v", c.in, c.wantErr, err)
		}
	}
}

func TestService_Create_Success(t *testing.T) {
	t.Parallel()
	var gotRow CreateRow
	repo := fakeRepo{
		tokenExistsFn: func(context.Context, string) (bool, error) { return false, nil },
		createFn:      func(_ context.Context, r CreateRow) error { gotRow = r; return nil },
	}
	svc := New(repo, &fakeState{}, WithIDGenerator(stubIDGen{id: "newid"}))

	in := CreateUserInput{
		Name:        "Alice",
		Token:       "tok-1",
		Webhook:     "https://hook/1",
		Events:      "Message,ReadReceipt",
		ProxyConfig: ProxyConfig{Enabled: true, ProxyURL: "socks5://prox:1080"},
		S3Config:    S3Config{Enabled: true, Bucket: "buck", AccessKey: "SECRETAK", SecretKey: "SECRETSK"},
	}
	u, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.ID != "newid" || u.Name != "Alice" {
		t.Errorf("returned: %+v", u)
	}
	if u.S3Config.AccessKey != "***" || u.S3Config.SecretKey != "" {
		t.Errorf("S3 secrets leaked in response: %+v", u.S3Config)
	}
	if !u.ProxyConfig.Enabled || u.ProxyConfig.ProxyURL != "socks5://prox:1080" {
		t.Errorf("proxy_config: %+v", u.ProxyConfig)
	}
	if gotRow.S3.AccessKey != "SECRETAK" || gotRow.S3.SecretKey != "SECRETSK" {
		t.Errorf("persisted row should carry raw secrets: %+v", gotRow.S3)
	}
	if gotRow.ID != "newid" {
		t.Errorf("persisted ID: %q", gotRow.ID)
	}
}

func TestService_Create_InvalidEvents(t *testing.T) {
	t.Parallel()
	svc := New(fakeRepo{}, &fakeState{})
	_, err := svc.Create(context.Background(), CreateUserInput{Events: "Bogus"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("err: want ErrInvalidEvent, got %v", err)
	}
}

func TestService_Create_HMACTooShort(t *testing.T) {
	t.Parallel()
	svc := New(fakeRepo{}, &fakeState{})
	_, err := svc.Create(context.Background(), CreateUserInput{HMACKey: "tooShort"})
	if !errors.Is(err, ErrHMACKeyTooShort) {
		t.Errorf("err: want ErrHMACKeyTooShort, got %v", err)
	}
}

func TestService_Create_HMACEncryptError(t *testing.T) {
	t.Parallel()
	want := errors.New("crypto down")
	svc := New(fakeRepo{}, &fakeState{}, WithHMACEncryptor(stubEncryptor{err: want}))
	_, err := svc.Create(context.Background(), CreateUserInput{HMACKey: "01234567890123456789012345678901xx"})
	if !errors.Is(err, want) {
		t.Errorf("err: want wrapping %v, got %v", want, err)
	}
}

func TestService_Create_HMACEncrypted(t *testing.T) {
	t.Parallel()
	var gotRow CreateRow
	repo := fakeRepo{createFn: func(_ context.Context, r CreateRow) error { gotRow = r; return nil }}
	enc := stubEncryptor{out: []byte{0xab, 0xcd}}
	svc := New(repo, &fakeState{}, WithIDGenerator(stubIDGen{id: "x"}), WithHMACEncryptor(enc))
	_, err := svc.Create(context.Background(), CreateUserInput{
		Token:   "t",
		HMACKey: "01234567890123456789012345678901xx",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(gotRow.HMACKey) != 2 || gotRow.HMACKey[0] != 0xab {
		t.Errorf("encrypted hmac bytes not persisted: %x", gotRow.HMACKey)
	}
}

func TestService_Create_TokenExists(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{tokenExistsFn: func(context.Context, string) (bool, error) { return true, nil }}
	svc := New(repo, &fakeState{})
	_, err := svc.Create(context.Background(), CreateUserInput{Token: "dup"})
	if !errors.Is(err, ErrTokenExists) {
		t.Errorf("err: want ErrTokenExists, got %v", err)
	}
}

func TestService_Create_TokenExistsError(t *testing.T) {
	t.Parallel()
	want := errors.New("db down")
	repo := fakeRepo{tokenExistsFn: func(context.Context, string) (bool, error) { return false, want }}
	svc := New(repo, &fakeState{})
	_, err := svc.Create(context.Background(), CreateUserInput{Token: "x"})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestService_Create_IDGenError(t *testing.T) {
	t.Parallel()
	want := errors.New("rand fail")
	svc := New(fakeRepo{}, &fakeState{}, WithIDGenerator(stubIDGen{err: want}))
	_, err := svc.Create(context.Background(), CreateUserInput{})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestService_Create_RepoError(t *testing.T) {
	t.Parallel()
	want := errors.New("insert fail")
	repo := fakeRepo{createFn: func(context.Context, CreateRow) error { return want }}
	svc := New(repo, &fakeState{}, WithIDGenerator(stubIDGen{id: "i"}))
	_, err := svc.Create(context.Background(), CreateUserInput{})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestDefaultIDGenerator_Hex32(t *testing.T) {
	t.Parallel()
	id, err := cryptoRandIDGen{}.Generate()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(id) != 32 {
		t.Errorf("len: want 32 hex chars, got %d (%q)", len(id), id)
	}
}

type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }

func TestDefaultIDGenerator_ReadError(t *testing.T) {
	t.Parallel()
	want := errors.New("entropy starved")
	_, err := cryptoRandIDGen{r: errReader{err: want}}.Generate()
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestDefaultEncryptor_Disabled(t *testing.T) {
	t.Parallel()
	_, err := disabledEncryptor{}.Encrypt("anything")
	if err == nil {
		t.Errorf("default encryptor should refuse")
	}
}

func TestHandler_Create_201(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{}
	h := newRouterHandler(New(repo, &fakeState{}, WithIDGenerator(stubIDGen{id: "abc"})))
	body := `{"name":"Alice","token":"t1","webhook":"https://hook","events":"Message","proxyConfig":{"enabled":true,"proxyURL":"socks5://p:1080"},"s3Config":{"enabled":true,"bucket":"buck","access_key":"AK","secret_key":"SK"}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    User `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != 201 || !env.Success {
		t.Errorf("envelope: %+v", env)
	}
	if env.Data.ID != "abc" || env.Data.S3Config.AccessKey != "***" {
		t.Errorf("data: %+v", env.Data)
	}
}

func TestHandler_Create_InvalidJSON_400(t *testing.T) {
	t.Parallel()
	h := newRouterHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestHandler_Create_TokenConflict_409(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{tokenExistsFn: func(context.Context, string) (bool, error) { return true, nil }}
	h := newRouterHandler(New(repo, &fakeState{}, WithIDGenerator(stubIDGen{id: "x"})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{"token":"dup"}`)))
	if rec.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d", rec.Code)
	}
}

func TestHandler_Create_InvalidEvents_400(t *testing.T) {
	t.Parallel()
	h := newRouterHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{"events":"Nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestHandler_Create_HMACTooShort_400(t *testing.T) {
	t.Parallel()
	h := newRouterHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{"hmacKey":"short"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestHandler_Create_DBError_500(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{createFn: func(context.Context, CreateRow) error { return errors.New("boom") }}
	h := newRouterHandler(New(repo, &fakeState{}, WithIDGenerator(stubIDGen{id: "x"})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", rec.Code)
	}
}

func TestBuildUpdateFields_AllFields(t *testing.T) {
	t.Parallel()
	in := UpdateInput{
		Name: "n", Token: "t", Webhook: "w", Expiration: 5, Events: "Message",
		History: 7,
		ProxyConfig: &ProxyConfig{Enabled: true, ProxyURL: "socks5://x"},
		S3Config:    &S3Config{Enabled: true, Bucket: "b"},
	}
	got := buildUpdateFields(in)
	wantCols := []string{
		"name", "token", "webhook", "expiration", "events", "history",
		"proxy_url",
		"s3_enabled", "s3_endpoint", "s3_region", "s3_bucket",
		"s3_access_key", "s3_secret_key", "s3_path_style", "s3_public_url",
		"media_delivery", "s3_retention_days",
	}
	if len(got) != len(wantCols) {
		t.Fatalf("len: want %d, got %d", len(wantCols), len(got))
	}
	for i, col := range wantCols {
		if got[i].Column != col {
			t.Errorf("[%d]: want %q, got %q", i, col, got[i].Column)
		}
	}
}

func TestBuildUpdateFields_ProxyDisabledClearsURL(t *testing.T) {
	t.Parallel()
	in := UpdateInput{ProxyConfig: &ProxyConfig{Enabled: false, ProxyURL: "ignored"}}
	got := buildUpdateFields(in)
	if len(got) != 1 || got[0].Column != "proxy_url" || got[0].Value != "" {
		t.Errorf("expected single proxy_url='': %+v", got)
	}
}

func TestBuildUpdateFields_Empty(t *testing.T) {
	t.Parallel()
	if got := buildUpdateFields(UpdateInput{}); len(got) != 0 {
		t.Errorf("empty input should yield 0 fields, got %d", len(got))
	}
}

func TestService_Update_Success(t *testing.T) {
	t.Parallel()
	var gotID string
	var gotFields []UpdateField
	repo := fakeRepo{
		existsByIDFn: func(context.Context, string) (bool, error) { return true, nil },
		getTokenFn:   func(context.Context, string) (string, error) { return "old-tok", nil },
		updateFn: func(_ context.Context, id string, f []UpdateField) error {
			gotID = id
			gotFields = f
			return nil
		},
	}
	hookCalls := 0
	var lastEv UpdateResult
	hook := func(_ context.Context, ev UpdateResult) {
		hookCalls++
		lastEv = ev
	}
	svc := New(repo, &fakeState{}, WithUpdateHook(hook))
	res, err := svc.Update(context.Background(), "u1", UpdateInput{Name: "Bob", Token: "new-tok"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotID != "u1" || len(gotFields) != 2 {
		t.Errorf("gotID=%q fields=%+v", gotID, gotFields)
	}
	if res.OldToken != "old-tok" || res.NewToken != "new-tok" {
		t.Errorf("tokens: %+v", res)
	}
	if hookCalls != 1 || lastEv.UserID != "u1" {
		t.Errorf("hook not called once: calls=%d ev=%+v", hookCalls, lastEv)
	}
}

func TestService_Update_KeepsOldTokenWhenNotPatched(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{
		getTokenFn: func(context.Context, string) (string, error) { return "same-tok", nil },
		updateFn:   func(context.Context, string, []UpdateField) error { return nil },
	}
	svc := New(repo, &fakeState{})
	res, err := svc.Update(context.Background(), "u", UpdateInput{Webhook: "https://x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.NewToken != "same-tok" {
		t.Errorf("NewToken should equal OldToken when token not patched: %+v", res)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{existsByIDFn: func(context.Context, string) (bool, error) { return false, nil }}
	svc := New(repo, &fakeState{})
	_, err := svc.Update(context.Background(), "x", UpdateInput{Name: "n"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err: want ErrNotFound, got %v", err)
	}
}

func TestService_Update_ExistsCheckError(t *testing.T) {
	t.Parallel()
	want := errors.New("db down")
	repo := fakeRepo{existsByIDFn: func(context.Context, string) (bool, error) { return false, want }}
	svc := New(repo, &fakeState{})
	_, err := svc.Update(context.Background(), "x", UpdateInput{Name: "n"})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestService_Update_InvalidEvents(t *testing.T) {
	t.Parallel()
	svc := New(fakeRepo{}, &fakeState{})
	_, err := svc.Update(context.Background(), "x", UpdateInput{Events: "Bogus"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("err: want ErrInvalidEvent, got %v", err)
	}
}

func TestService_Update_GetTokenError(t *testing.T) {
	t.Parallel()
	want := errors.New("net split")
	repo := fakeRepo{getTokenFn: func(context.Context, string) (string, error) { return "", want }}
	svc := New(repo, &fakeState{})
	_, err := svc.Update(context.Background(), "x", UpdateInput{Name: "n"})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestService_Update_NoFields(t *testing.T) {
	t.Parallel()
	svc := New(fakeRepo{}, &fakeState{})
	_, err := svc.Update(context.Background(), "x", UpdateInput{})
	if !errors.Is(err, ErrNoFields) {
		t.Errorf("err: want ErrNoFields, got %v", err)
	}
}

func TestService_Update_RepoError(t *testing.T) {
	t.Parallel()
	want := errors.New("update boom")
	repo := fakeRepo{updateFn: func(context.Context, string, []UpdateField) error { return want }}
	svc := New(repo, &fakeState{})
	_, err := svc.Update(context.Background(), "x", UpdateInput{Name: "n"})
	if !errors.Is(err, want) {
		t.Errorf("err: want %v, got %v", want, err)
	}
}

func TestHandler_Update_200(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{updateFn: func(context.Context, string, []UpdateField) error { return nil }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/u1", strings.NewReader(`{"name":"Bob"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code    int    `json:"code"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if !env.Success || env.Message == "" {
		t.Errorf("envelope: %+v", env)
	}
}

func TestHandler_Update_S3AndProxy_200(t *testing.T) {
	t.Parallel()
	var gotFields []UpdateField
	repo := fakeRepo{updateFn: func(_ context.Context, _ string, f []UpdateField) error { gotFields = f; return nil }}
	h := newRouterHandler(New(repo, &fakeState{}))
	body := `{"proxyConfig":{"enabled":true,"proxyURL":"socks5://p"},"s3Config":{"enabled":false,"bucket":"b"}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/u1", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	hasProxy, hasS3 := false, false
	for _, f := range gotFields {
		if f.Column == "proxy_url" {
			hasProxy = true
		}
		if f.Column == "s3_enabled" {
			hasS3 = true
		}
	}
	if !hasProxy || !hasS3 {
		t.Errorf("missing fields: proxy=%v s3=%v gotFields=%+v", hasProxy, hasS3, gotFields)
	}
}

func TestHandler_Update_MissingID_400(t *testing.T) {
	t.Parallel()
	h := NewHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestHandler_Update_InvalidJSON_400(t *testing.T) {
	t.Parallel()
	h := newRouterHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/u1", strings.NewReader(`{not json`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", rec.Code)
	}
}

func TestHandler_Update_NotFound_404(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{existsByIDFn: func(context.Context, string) (bool, error) { return false, nil }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/missing", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestHandler_Update_InvalidEvents_400(t *testing.T) {
	t.Parallel()
	h := newRouterHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/u1", strings.NewReader(`{"events":"Bogus"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestHandler_Update_NoFields_400(t *testing.T) {
	t.Parallel()
	h := newRouterHandler(New(fakeRepo{}, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/u1", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestHandler_Update_DBError_500(t *testing.T) {
	t.Parallel()
	repo := fakeRepo{updateFn: func(context.Context, string, []UpdateField) error { return errors.New("boom") }}
	h := newRouterHandler(New(repo, &fakeState{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/users/u1", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rec.Code)
	}
}
