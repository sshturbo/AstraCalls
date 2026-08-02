package main

import (
	"context"
	"net/http"
	"strings"
)

// withAdminAuth adiciona as rotas públicas de bootstrap/login e protege as
// demais rotas da API. A API key continua disponível para integrações; o JWT é
// destinado ao Manager v2 e representa o único administrador cadastrado.
func (s *server) withAdminAuth(next http.Handler, apiKey string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/api/auth/status":
			s.handleAuthStatus(w, r)
			return
		case r.Method == http.MethodPost && path == "/api/auth/setup":
			s.handleAuthSetup(w, r)
			return
		case r.Method == http.MethodPost && path == "/api/auth/login":
			s.handleAuthLoginRateLimited(w, r)
			return
		}

		guarded := strings.HasPrefix(path, "/api/") && !strings.HasSuffix(path, "/chatwoot/webhook")
		if !guarded {
			next.ServeHTTP(w, r)
			return
		}

		claims, authenticated := s.auth.authenticateRequest(r, apiKey)
		if !authenticated {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		ctx := context.WithValue(r.Context(), authContextKey{}, claims)
		r = r.WithContext(ctx)

		if r.Method == http.MethodGet && path == "/api/auth/me" {
			s.handleAuthMe(w, r)
			return
		}

		// routes() mantém a proteção antiga por API key para compatibilidade.
		// Ao autenticar por JWT, injeta a chave apenas dentro do processo para o
		// middleware legado aceitar a chamada, sem entregá-la ao navegador.
		if claims.Role == "admin" && apiKey != "" {
			r.Header.Set("X-API-Key", apiKey)
		}
		next.ServeHTTP(w, r)
	})
}
