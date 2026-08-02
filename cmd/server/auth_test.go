package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestAuthService(t *testing.T) (*authService, *server) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := newAdminStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := &authService{
		store:  store,
		secret: []byte("test-secret-with-at-least-thirty-two-characters"),
		ttl:    time.Hour,
		log:    logger,
	}
	return auth, &server{auth: auth, log: logger}
}

func TestSingleAdministratorSetupAndLogin(t *testing.T) {
	auth, _ := newTestAuthService(t)
	ctx := context.Background()

	initialized, err := auth.store.initialized(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		t.Fatal("administrator must not exist before setup")
	}

	token, claims, err := auth.setup(ctx, " Admin ", "very-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || claims.Username != "admin" || claims.Role != "admin" {
		t.Fatalf("unexpected setup result: token=%t claims=%+v", token != "", claims)
	}
	verified, err := auth.verifyToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Username != "admin" || verified.Subject != "admin" {
		t.Fatalf("unexpected verified claims: %+v", verified)
	}

	tampered := token[:len(token)-1] + "A"
	if _, err := auth.verifyToken(tampered); !errors.Is(err, errInvalidToken) {
		t.Fatalf("tampered JWT must be rejected, got %v", err)
	}

	if _, _, err := auth.setup(ctx, "other", "another-strong-password"); !errors.Is(err, errAdminAlreadyConfigured) {
		t.Fatalf("expected single-admin conflict, got %v", err)
	}
	if _, _, err := auth.login(ctx, "admin", "wrong-password-value"); !errors.Is(err, errInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
	loginToken, loginClaims, err := auth.login(ctx, "ADMIN", "very-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if loginToken == "" || loginClaims.Username != "admin" {
		t.Fatalf("unexpected login result: %+v", loginClaims)
	}
}

func TestAdministratorJWTAndAPIKeyMiddleware(t *testing.T) {
	auth, srv := newTestAuthService(t)
	token, _, err := auth.setup(context.Background(), "admin", "very-strong-password")
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromRequest(r)
		if !ok {
			t.Error("authenticated request has no claims")
		}
		if claims.Role == "admin" && r.Header.Get("X-API-Key") != "integration-secret" {
			t.Error("JWT request did not receive internal API key bridge")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := srv.withAdminAuth(next, "integration-secret")

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}

	jwtRequest := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	jwtRequest.Header.Set("Authorization", "Bearer "+token)
	jwtResponse := httptest.NewRecorder()
	handler.ServeHTTP(jwtResponse, jwtRequest)
	if jwtResponse.Code != http.StatusNoContent {
		t.Fatalf("expected JWT request to pass, got %d", jwtResponse.Code)
	}

	apiKeyRequest := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	apiKeyRequest.Header.Set("X-API-Key", "integration-secret")
	apiKeyResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiKeyResponse, apiKeyRequest)
	if apiKeyResponse.Code != http.StatusNoContent {
		t.Fatalf("expected API key request to pass, got %d", apiKeyResponse.Code)
	}
}

func TestAuthHTTPBootstrapIsPublicAndUnique(t *testing.T) {
	_, srv := newTestAuthService(t)
	handler := srv.withAdminAuth(http.NotFoundHandler(), "")

	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"initialized":false`) {
		t.Fatalf("unexpected initial status: %d %s", statusResponse.Code, statusResponse.Body.String())
	}

	setupBody := `{"username":"admin","password":"very-strong-password"}`
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(setupBody)))
	if setupResponse.Code != http.StatusCreated || !strings.Contains(setupResponse.Body.String(), `"token"`) {
		t.Fatalf("unexpected setup response: %d %s", setupResponse.Code, setupResponse.Body.String())
	}

	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(setupBody)))
	if secondResponse.Code != http.StatusConflict {
		t.Fatalf("second administrator must be rejected, got %d", secondResponse.Code)
	}
}
