package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminUserID      = 1
	jwtIssuer        = "astracalls"
	jwtAudience      = "manager-v2"
	jwtSecretKeyName = "jwt_secret_v1"
	minimumPassword  = 12
	maximumPassword  = 128
)

type adminUser struct {
	ID           int
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type adminStore struct {
	db *sql.DB
}

func newAdminStore(ctx context.Context, db *sql.DB) (*adminStore, error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS admin_user (
		id            SMALLINT PRIMARY KEY CHECK (id = 1),
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return nil, fmt.Errorf("create admin_user table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS app_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return nil, fmt.Errorf("create app_settings table: %w", err)
	}
	return &adminStore{db: db}, nil
}

func (s *adminStore) initialized(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_user WHERE id = $1)`, adminUserID).Scan(&exists)
	return exists, err
}

func (s *adminStore) create(ctx context.Context, username, passwordHash string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_user (id, username, password_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, adminUserID, username, passwordHash)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *adminStore) findByUsername(ctx context.Context, username string) (adminUser, error) {
	var user adminUser
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at
		FROM admin_user
		WHERE id = $1 AND username = $2
	`, adminUserID, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt)
	return user, err
}

func (s *adminStore) setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&value)
	return value, err
}

func (s *adminStore) createSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING
	`, key, value)
	return err
}

type jwtClaims struct {
	Subject  string `json:"sub"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
	JWTID    string `json:"jti"`
}

type authContextKey struct{}

type authService struct {
	store  *adminStore
	secret []byte
	ttl    time.Duration
	log    *slog.Logger
}

func newAuthService(ctx context.Context, db *sql.DB, log *slog.Logger) (*authService, error) {
	store, err := newAdminStore(ctx, db)
	if err != nil {
		return nil, err
	}
	secret, err := loadJWTSecret(ctx, store)
	if err != nil {
		return nil, err
	}
	ttlHours := envInt("WACALLS_JWT_TTL_HOURS", 12)
	if ttlHours < 1 {
		ttlHours = 1
	}
	if ttlHours > 168 {
		ttlHours = 168
	}
	return &authService{store: store, secret: secret, ttl: time.Duration(ttlHours) * time.Hour, log: log}, nil
}

func loadJWTSecret(ctx context.Context, store *adminStore) ([]byte, error) {
	if configured := strings.TrimSpace(os.Getenv("WACALLS_JWT_SECRET")); configured != "" {
		if len(configured) < 32 {
			return nil, fmt.Errorf("WACALLS_JWT_SECRET must contain at least 32 characters")
		}
		return []byte(configured), nil
	}

	stored, err := store.setting(ctx, jwtSecretKeyName)
	if err == nil {
		secret, decodeErr := base64.RawURLEncoding.DecodeString(stored)
		if decodeErr != nil || len(secret) < 32 {
			return nil, fmt.Errorf("stored JWT secret is invalid")
		}
		return secret, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read JWT secret: %w", err)
	}

	secret := make([]byte, 48)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate JWT secret: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	if err := store.createSetting(ctx, jwtSecretKeyName, encoded); err != nil {
		return nil, fmt.Errorf("persist JWT secret: %w", err)
	}

	// Outra réplica pode ter vencido a corrida do INSERT. Sempre relê o valor
	// efetivamente persistido para que todas assinem com a mesma chave.
	persisted, err := store.setting(ctx, jwtSecretKeyName)
	if err != nil {
		return nil, fmt.Errorf("reload JWT secret: %w", err)
	}
	secret, err = base64.RawURLEncoding.DecodeString(persisted)
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("persisted JWT secret is invalid")
	}
	return secret, nil
}

func normalizeAdminUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateAdminCredentials(username, password string) error {
	if len(username) < 3 || len(username) > 64 {
		return fmt.Errorf("username must contain between 3 and 64 characters")
	}
	if strings.ContainsAny(username, "\r\n\t") {
		return fmt.Errorf("username contains invalid characters")
	}
	if len(password) < minimumPassword || len(password) > maximumPassword {
		return fmt.Errorf("password must contain between %d and %d characters", minimumPassword, maximumPassword)
	}
	return nil
}

func (a *authService) setup(ctx context.Context, username, password string) (string, jwtClaims, error) {
	username = normalizeAdminUsername(username)
	if err := validateAdminCredentials(username, password); err != nil {
		return "", jwtClaims{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", jwtClaims{}, fmt.Errorf("hash password: %w", err)
	}
	created, err := a.store.create(ctx, username, string(hash))
	if err != nil {
		return "", jwtClaims{}, err
	}
	if !created {
		return "", jwtClaims{}, errAdminAlreadyConfigured
	}
	return a.issueToken(username)
}

func (a *authService) login(ctx context.Context, username, password string) (string, jwtClaims, error) {
	username = normalizeAdminUsername(username)
	user, err := a.store.findByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", jwtClaims{}, errInvalidCredentials
		}
		return "", jwtClaims{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", jwtClaims{}, errInvalidCredentials
	}
	return a.issueToken(user.Username)
}

func (a *authService) issueToken(username string) (string, jwtClaims, error) {
	now := time.Now().UTC()
	jtiBytes := make([]byte, 18)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", jwtClaims{}, err
	}
	claims := jwtClaims{
		Subject:  "admin",
		Username: username,
		Role:     "admin",
		Issuer:   jwtIssuer,
		Audience: jwtAudience,
		IssuedAt: now.Unix(),
		Expires:  now.Add(a.ttl).Unix(),
		JWTID:    base64.RawURLEncoding.EncodeToString(jtiBytes),
	}
	headerJSON := []byte(`{"alg":"HS256","typ":"JWT"}`)
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", jwtClaims{}, err
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, claims, nil
}

func (a *authService) verifyToken(token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errInvalidToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(headerJSON, &header) != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return jwtClaims{}, errInvalidToken
	}

	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(actual, expected) {
		return jwtClaims{}, errInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, errInvalidToken
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtClaims{}, errInvalidToken
	}
	now := time.Now().Unix()
	if claims.Subject != "admin" || claims.Role != "admin" || claims.Issuer != jwtIssuer || claims.Audience != jwtAudience || claims.JWTID == "" {
		return jwtClaims{}, errInvalidToken
	}
	if claims.IssuedAt > now+60 || claims.Expires <= now {
		return jwtClaims{}, errInvalidToken
	}
	return claims, nil
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func sameSecret(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return hmac.Equal([]byte(left), []byte(right))
}

func (a *authService) authenticateRequest(r *http.Request, apiKey string) (jwtClaims, bool) {
	providedAPIKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if providedAPIKey == "" {
		// Mantido para compatibilidade com clientes antigos de SSE. O Manager v2
		// novo usa Authorization e não envia mais segredos pela URL.
		providedAPIKey = strings.TrimSpace(r.URL.Query().Get("apiKey"))
	}
	if sameSecret(providedAPIKey, apiKey) {
		return jwtClaims{Subject: "api-key", Role: "integration", Issuer: jwtIssuer, Audience: "api"}, true
	}
	token := bearerToken(r)
	if token == "" {
		return jwtClaims{}, false
	}
	claims, err := a.verifyToken(token)
	return claims, err == nil
}

func claimsFromRequest(r *http.Request) (jwtClaims, bool) {
	claims, ok := r.Context().Value(authContextKey{}).(jwtClaims)
	return claims, ok
}

var (
	errAdminAlreadyConfigured = errors.New("administrator already configured")
	errInvalidCredentials     = errors.New("invalid credentials")
	errInvalidToken           = errors.New("invalid token")
)

func decodeAuthBody(r *http.Request) (string, string, error) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return "", "", fmt.Errorf("invalid JSON body")
	}
	return body.Username, body.Password, nil
}

func (s *server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.auth.store.initialized(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read authentication status"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"initialized": initialized,
		"mode":        "single-admin",
		"minPassword": minimumPassword,
	})
}

func (s *server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	username, password, err := decodeAuthBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	token, claims, err := s.auth.setup(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, errAdminAlreadyConfigured) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("administrator configured", "username", claims.Username)
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     token,
		"expiresAt": claims.Expires,
		"user":      map[string]string{"username": claims.Username, "role": claims.Role},
	})
}

func (s *server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	username, password, err := decodeAuthBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	token, claims, err := s.auth.login(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, errInvalidCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": claims.Expires,
		"user":      map[string]string{"username": claims.Username, "role": claims.Role},
	})
}

func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFromRequest(r)
	if !ok || claims.Role != "admin" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":  claims.Username,
		"role":      claims.Role,
		"expiresAt": claims.Expires,
	})
}

func authTokenTTLLabel(seconds int64) string {
	return strconv.FormatInt(seconds, 10)
}
