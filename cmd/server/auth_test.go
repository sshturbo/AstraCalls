package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
		store:          store,
		secret:         []byte("test-secret-with-at-least-thirty-two-characters"),
		ttl:            time.Hour,
		apiToken:       "generated-general-token",
		apiTokenSource: "generated",
		log:            logger,
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
	if token == "" || claims.Username != "admin" || claims.Role != "admin" || claims.Version != 1 {
		t.Fatalf("unexpected setup result: token=%t claims=%+v", token != "", claims)
	}
	verified, err := auth.verifyToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Username != "admin" || verified.Subject != "admin" {
		t.Fatalf("unexpected verified claims: %+v", verified)
	}

	replacement := "A"
	if strings.HasSuffix(token, replacement) {
		replacement = "B"
	}
	tampered := token[:len(token)-1] + replacement
	if _, err := auth.verifyToken(ctx, tampered); !errors.Is(err, errInvalidToken) {
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

func TestAdministratorProfileUpdateInvalidatesOldTokens(t *testing.T) {
	auth, _ := newTestAuthService(t)
	ctx := context.Background()

	originalToken, _, err := auth.setup(ctx, "admin", "very-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.updateProfile(ctx, "wrong-password-value", "owner", ""); !errors.Is(err, errInvalidCredentials) {
		t.Fatalf("expected current password validation, got %v", err)
	}

	renamedToken, renamedClaims, err := auth.updateProfile(ctx, "very-strong-password", " Owner ", "")
	if err != nil {
		t.Fatal(err)
	}
	if renamedClaims.Username != "owner" || renamedClaims.Version != 2 {
		t.Fatalf("unexpected renamed claims: %+v", renamedClaims)
	}
	if _, err := auth.verifyToken(ctx, originalToken); !errors.Is(err, errInvalidToken) {
		t.Fatalf("old token must be invalid after username change, got %v", err)
	}
	if _, err := auth.verifyToken(ctx, renamedToken); err != nil {
		t.Fatalf("replacement token must be valid: %v", err)
	}
	if _, _, err := auth.login(ctx, "admin", "very-strong-password"); !errors.Is(err, errInvalidCredentials) {
		t.Fatalf("old username must stop working, got %v", err)
	}

	passwordToken, passwordClaims, err := auth.updateProfile(ctx, "very-strong-password", "owner", "another-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if passwordClaims.Version != 3 {
		t.Fatalf("expected token version 3, got %+v", passwordClaims)
	}
	if _, err := auth.verifyToken(ctx, renamedToken); !errors.Is(err, errInvalidToken) {
		t.Fatalf("previous token must be invalid after password change, got %v", err)
	}
	if _, err := auth.verifyToken(ctx, passwordToken); err != nil {
		t.Fatalf("new password token must be valid: %v", err)
	}
	if _, _, err := auth.login(ctx, "owner", "very-strong-password"); !errors.Is(err, errInvalidCredentials) {
		t.Fatalf("old password must stop working, got %v", err)
	}
	if _, _, err := auth.login(ctx, "owner", "another-strong-password"); err != nil {
		t.Fatalf("new credentials must work: %v", err)
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

func TestGeneratedGeneralTokenAuthenticatesIntegrations(t *testing.T) {
	auth, srv := newTestAuthService(t)
	handler := srv.withAdminAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "")

	request := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	request.Header.Set("X-API-Key", auth.apiToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("generated general token must authenticate, got %d", response.Code)
	}
}

func TestProfileEndpointRequiresAdministratorJWT(t *testing.T) {
	auth, srv := newTestAuthService(t)
	token, _, err := auth.setup(context.Background(), "admin", "very-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.withAdminAuth(http.NotFoundHandler(), "integration-secret")

	integrationRequest := httptest.NewRequest(http.MethodGet, "/api/auth/profile", nil)
	integrationRequest.Header.Set("X-API-Key", "integration-secret")
	integrationResponse := httptest.NewRecorder()
	handler.ServeHTTP(integrationResponse, integrationRequest)
	if integrationResponse.Code != http.StatusForbidden {
		t.Fatalf("integration token must not expose administrator profile, got %d", integrationResponse.Code)
	}

	adminRequest := httptest.NewRequest(http.MethodGet, "/api/auth/profile", nil)
	adminRequest.Header.Set("Authorization", "Bearer "+token)
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || !strings.Contains(adminResponse.Body.String(), "generated-general-token") {
		t.Fatalf("administrator profile must expose general token: %d %s", adminResponse.Code, adminResponse.Body.String())
	}
}

func TestAuthHTTPBootstrapIsPublicAndUnique(t *testing.T) {
	_, srv := newTestAuthService(t)
	handler := srv.withAdminAuth(http.NotFoundHandler(), "")

	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), "\"initialized\":false") {
		t.Fatalf("unexpected initial status: %d %s", statusResponse.Code, statusResponse.Body.String())
	}

	setupJSON, err := json.Marshal(map[string]string{
		"username": "admin",
		"password": "very-strong-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(string(setupJSON))))
	if setupResponse.Code != http.StatusCreated || !strings.Contains(setupResponse.Body.String(), "\"token\"") {
		t.Fatalf("unexpected setup response: %d %s", setupResponse.Code, setupResponse.Body.String())
	}

	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(string(setupJSON))))
	if secondResponse.Code != http.StatusConflict {
		t.Fatalf("second administrator must be rejected, got %d", secondResponse.Code)
	}
}
