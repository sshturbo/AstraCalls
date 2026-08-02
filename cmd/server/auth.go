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
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminUserID       = 1
	jwtIssuer         = "astracalls"
	jwtAudience       = "manager-v2"
	jwtSecretKeyName  = "jwt_secret_v1"
	generalTokenName  = "general_api_token_v1"
	minimumPassword   = 12
	maximumPassword   = 128
	maximumAuthBody   = 8 << 10
	generatedTokenLen = 32
)

type adminUser struct {
	ID           int
	Username     string
	PasswordHash string
	TokenVersion int64
}

type adminStore struct {
	db *sql.DB
}

func newAdminStore(ctx context.Context, db *sql.DB) (*adminStore, error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS admin_user (
		id            SMALLINT PRIMARY KEY CHECK (id = 1),
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		token_version BIGINT NOT NULL DEFAULT 1,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return nil, fmt.Errorf("create admin_user table: %w", err)
	}
	if err := ensureAdminTokenVersion(ctx, db); err != nil {
		return nil, err
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

func ensureAdminTokenVersion(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT token_version FROM admin_user WHERE 1 = 0`)
	if err == nil {
		_ = rows.Close()
		return nil
	}
	if _, alterErr := db.ExecContext(ctx, `ALTER TABLE admin_user ADD COLUMN token_version BIGINT NOT NULL DEFAULT 1`); alterErr != nil {
		return fmt.Errorf("add admin token version: %w", alterErr)
	}
	return nil
}

func (s *adminStore) initialized(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_user WHERE id = $1)`, adminUserID).Scan(&exists)
	return exists, err
}

func (s *adminStore) create(ctx context.Context, username, passwordHash string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_user (id, username, password_hash, token_version)
		VALUES ($1, $2, $3, 1)
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
		SELECT id, username, password_hash, token_version
		FROM admin_user
		WHERE id = $1 AND username = $2
	`, adminUserID, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TokenVersion)
	return user, err
}

func (s *adminStore) current(ctx context.Context) (adminUser, error) {
	var user adminUser
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, token_version
		FROM admin_user
		WHERE id = $1
	`, adminUserID).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TokenVersion)
	return user, err
}

func (s *adminStore) updateProfile(ctx context.Context, username, passwordHash string) (adminUser, error) {
	if passwordHash == "" {
		_, err := s.db.ExecContext(ctx, `
			UPDATE admin_user
			SET username = $2, token_version = token_version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
		`, adminUserID, username)
		if err != nil {
			return adminUser{}, err
		}
	} else {
		_, err := s.db.ExecContext(ctx, `
			UPDATE admin_user
			SET username = $2, password_hash = $3, token_version = token_version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
		`, adminUserID, username, passwordHash)
		if err != nil {
			return adminUser{}, err
		}
	}
	return s.current(ctx)
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
	Version  int64  `json:"ver"`
}

type authContextKey struct{}

type authService struct {
	store          *adminStore
	secret         []byte
	ttl            time.Duration
	apiToken       string
	apiTokenSource string
	log            *slog.Logger
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
	apiToken, apiTokenSource, err := loadGeneralAPIToken(ctx, store)
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
	return &authService{
		store:          store,
		secret:         secret,
		ttl:            time.Duration(ttlHours) * time.Hour,
		apiToken:       apiToken,
		apiTokenSource: apiTokenSource,
		log:            log,
	}, nil
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

func loadGeneralAPIToken(ctx context.Context, store *adminStore) (string, string, error) {
	if configured := strings.TrimSpace(os.Getenv("WACALLS_API_KEY")); configured != "" {
		return configured, "environment", nil
	}
	stored, err := store.setting(ctx, generalTokenName)
	if err == nil {
		if strings.TrimSpace(stored) == "" {
			return "", "", fmt.Errorf("stored general API token is invalid")
		}
		return stored, "generated", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("read general API token: %w", err)
	}

	raw := make([]byte, generatedTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate general API token: %w", err)
	}
	generated := "wc_" + base64.RawURLEncoding.EncodeToString(raw)
	if err := store.createSetting(ctx, generalTokenName, generated); err != nil {
		return "", "", fmt.Errorf("persist general API token: %w", err)
	}
	persisted, err := store.setting(ctx, generalTokenName)
	if err != nil {
		return "", "", fmt.Errorf("reload general API token: %w", err)
	}
	return persisted, "generated", nil
}

func normalizeAdminUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateAdminUsername(username string) error {
	if len(username) < 3 || len(username) > 64 {
		return fmt.Errorf("username must contain between 3 and 64 characters")
	}
	if strings.ContainsAny(username, "\r\n\t") {
		return fmt.Errorf("username contains invalid characters")
	}
	return nil
}

func validateAdminPassword(password string) error {
	if len(password) < minimumPassword || len(password) > maximumPassword {
		return fmt.Errorf("password must contain between %d and %d characters", minimumPassword, maximumPassword)
	}
	return nil
}

func validateAdminCredentials(username, password string) error {
	if err := validateAdminUsername(username); err != nil {
		return err
	}
	return validateAdminPassword(password)
}

func (a *authService) setup(ctx context.Context, username, password string) (string, jwtClaims, error) {
	username = normalizeAdminUsername(username)
	if err := validateAdminCredentials(username, password); err != nil {
		return "", jwtClaims{}, err
	}
	initialized, err := a.store.initialized(ctx)
	if err != nil {
		return "", jwtClaims{}, err
	}
	if initialized {
		return "", jwtClaims{}, errAdminAlreadyConfigured
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
	user, err := a.store.current(ctx)
	if err != nil {
		return "", jwtClaims{}, err
	}
	return a.issueToken(user)
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
	return a.issueToken(user)
}

func (a *authService) updateProfile(ctx context.Context, currentPassword, username, newPassword string) (string, jwtClaims, error) {
	user, err := a.store.current(ctx)
	if err != nil {
		return "", jwtClaims{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)) != nil {
		return "", jwtClaims{}, errInvalidCredentials
	}

	username = normalizeAdminUsername(username)
	if username == "" {
		username = user.Username
	}
	if err := validateAdminUsername(username); err != nil {
		return "", jwtClaims{}, err
	}
	if newPassword != "" {
		if err := validateAdminPassword(newPassword); err != nil {
			return "", jwtClaims{}, err
		}
	}
	if username == user.Username && newPassword == "" {
		return "", jwtClaims{}, errNoProfileChanges
	}

	passwordHash := ""
	if newPassword != "" {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
		if hashErr != nil {
			return "", jwtClaims{}, fmt.Errorf("hash password: %w", hashErr)
		}
		passwordHash = string(hash)
	}
	updated, err := a.store.updateProfile(ctx, username, passwordHash)
	if err != nil {
		return "", jwtClaims{}, err
	}
	return a.issueToken(updated)
}

func (a *authService) issueToken(user adminUser) (string, jwtClaims, error) {
	now := time.Now().UTC()
	jtiBytes := make([]byte, 18)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", jwtClaims{}, err
	}
	claims := jwtClaims{
		Subject:  "admin",
		Username: user.Username,
		Role:     "admin",
		Issuer:   jwtIssuer,
		Audience: jwtAudience,
		IssuedAt: now.Unix(),
		Expires:  now.Add(a.ttl).Unix(),
		JWTID:    base64.RawURLEncoding.EncodeToString(jtiBytes),
		Version:  user.TokenVersion,
	}
	headerJSON, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", jwtClaims{}, err
	}
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

func (a *authService) verifyToken(ctx context.Context, token string) (jwtClaims, error) {
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
	if claims.Subject != "admin" || claims.Role != "admin" || claims.Issuer != jwtIssuer || claims.Audience != jwtAudience || claims.JWTID == "" || claims.Version < 1 {
		return jwtClaims{}, errInvalidToken
	}
	if claims.IssuedAt > now+60 || claims.Expires <= now {
		return jwtClaims{}, errInvalidToken
	}
	user, err := a.store.current(ctx)
	if err != nil || user.Username != claims.Username || user.TokenVersion != claims.Version {
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
	effectiveAPIKey := strings.TrimSpace(apiKey)
	if effectiveAPIKey == "" {
		effectiveAPIKey = a.apiToken
	}
	providedAPIKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if providedAPIKey == "" {
		providedAPIKey = strings.TrimSpace(r.URL.Query().Get("apiKey"))
	}
	if sameSecret(providedAPIKey, effectiveAPIKey) {
		return jwtClaims{Subject: "api-key", Role: "integration", Issuer: jwtIssuer, Audience: "api"}, true
	}
	token := bearerToken(r)
	if token == "" {
		return jwtClaims{}, false
	}
	claims, err := a.verifyToken(r.Context(), token)
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
	errNoProfileChanges       = errors.New("no profile changes requested")
)

func decodeAuthBody(r *http.Request) (string, string, error) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maximumAuthBody+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return "", "", fmt.Errorf("invalid JSON body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", "", fmt.Errorf("invalid JSON body")
	}
	return body.Username, body.Password, nil
}

func decodeProfileBody(r *http.Request) (string, string, string, error) {
	var body struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maximumAuthBody+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return "", "", "", fmt.Errorf("invalid JSON body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", "", "", fmt.Errorf("invalid JSON body")
	}
	return body.Username, body.CurrentPassword, body.NewPassword, nil
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

func (s *server) handleAuthProfile(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFromRequest(r)
	if !ok || claims.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator access required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":       claims.Username,
		"role":           claims.Role,
		"expiresAt":      claims.Expires,
		"generalToken":   s.auth.apiToken,
		"generalTokenBy": s.auth.apiTokenSource,
	})
}

func (s *server) handleAuthProfileUpdate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFromRequest(r)
	if !ok || claims.Role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "administrator access required"})
		return
	}
	username, currentPassword, newPassword, err := decodeProfileBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if currentPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current password required"})
		return
	}
	token, updatedClaims, err := s.auth.updateProfile(r.Context(), currentPassword, username, newPassword)
	if err != nil {
		switch {
		case errors.Is(err, errInvalidCredentials):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "current password is invalid"})
		case errors.Is(err, errNoProfileChanges):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	s.log.Info("administrator profile updated", "username", updatedClaims.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": updatedClaims.Expires,
		"user":      map[string]string{"username": updatedClaims.Username, "role": updatedClaims.Role},
	})
}
