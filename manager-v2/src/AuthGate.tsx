import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { KeyRound, LoaderCircle, LogOut, Server, ShieldCheck, UserRound } from "lucide-react";
import { App } from "./App";
import { AUTH_EXPIRED_EVENT, AUTH_TOKEN_STORAGE_KEY, apiRequest } from "./api";
import type { ApiSettings } from "./types";
import "./auth.css";

const SETTINGS_STORAGE_KEY = "astracalls-manager-v2-settings";

type AuthPhase = "loading" | "setup" | "login" | "ready" | "offline";

type AuthResponse = {
  token?: string;
  expiresAt?: number;
  user?: { username?: string; role?: string };
  error?: string;
};

const readBaseUrl = () => {
  try {
    const raw = localStorage.getItem(SETTINGS_STORAGE_KEY);
    if (!raw) return "";
    const parsed = JSON.parse(raw) as { baseUrl?: unknown };
    return typeof parsed.baseUrl === "string" ? parsed.baseUrl : "";
  } catch {
    return "";
  }
};

const persistBaseUrl = (baseUrl: string) => {
  localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify({ baseUrl }));
};

const settingsFor = (baseUrl: string): ApiSettings => ({
  baseUrl: baseUrl.trim().replace(/\/+$/, ""),
  apiKey: "",
});

export const AuthGate = () => {
  const initialBaseUrl = useMemo(readBaseUrl, []);
  const [baseUrl, setBaseUrl] = useState(initialBaseUrl);
  const [phase, setPhase] = useState<AuthPhase>("loading");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [currentUser, setCurrentUser] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const checkAuthentication = useCallback(async () => {
    setPhase("loading");
    setMessage("");
    const settings = settingsFor(baseUrl);
    persistBaseUrl(settings.baseUrl);

    const status = await apiRequest(settings, "GET", "/api/auth/status");
    if (!status.ok) {
      setMessage(
        status.status === 0
          ? "Não foi possível conectar ao servidor. Verifique a URL da API."
          : `Falha ao consultar autenticação: ${status.status} ${status.statusText}`,
      );
      setPhase("offline");
      return;
    }

    const statusData = status.data as { initialized?: boolean };
    if (!statusData.initialized) {
      sessionStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
      setPhase("setup");
      return;
    }

    const token = sessionStorage.getItem(AUTH_TOKEN_STORAGE_KEY);
    if (!token) {
      setPhase("login");
      return;
    }

    const me = await apiRequest(settings, "GET", "/api/auth/me");
    if (!me.ok) {
      sessionStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
      setPhase("login");
      return;
    }
    const user = me.data as { username?: string };
    setCurrentUser(user.username ?? "admin");
    setPhase("ready");
  }, [baseUrl]);

  useEffect(() => {
    void checkAuthentication();
  }, [checkAuthentication]);

  useEffect(() => {
    const handleExpired = () => {
      setCurrentUser("");
      setMessage("Sua sessão expirou. Entre novamente.");
      setPhase("login");
    };
    window.addEventListener(AUTH_EXPIRED_EVENT, handleExpired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, handleExpired);
  }, []);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setMessage("");

    if (phase === "setup" && password !== confirmPassword) {
      setMessage("A confirmação da senha não corresponde.");
      return;
    }
    if (username.trim().length < 3) {
      setMessage("O usuário deve ter pelo menos 3 caracteres.");
      return;
    }
    if (password.length < 12) {
      setMessage("A senha deve ter pelo menos 12 caracteres.");
      return;
    }

    setBusy(true);
    const settings = settingsFor(baseUrl);
    persistBaseUrl(settings.baseUrl);
    const endpoint = phase === "setup" ? "/api/auth/setup" : "/api/auth/login";
    const result = await apiRequest(settings, "POST", endpoint, {
      username: username.trim(),
      password,
    });
    setBusy(false);

    const data = (result.data ?? {}) as AuthResponse;
    if (!result.ok || !data.token) {
      if (result.status === 409) {
        setMessage("O administrador já foi criado. Faça login.");
        setPhase("login");
      } else if (result.status === 401) {
        setMessage("Usuário ou senha inválidos.");
      } else {
        setMessage(data.error || `Falha na autenticação (${result.status || "rede"}).`);
      }
      return;
    }

    sessionStorage.setItem(AUTH_TOKEN_STORAGE_KEY, data.token);
    setCurrentUser(data.user?.username ?? username.trim().toLowerCase());
    setPassword("");
    setConfirmPassword("");
    setPhase("ready");
  };

  const logout = () => {
    sessionStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
    setCurrentUser("");
    setPassword("");
    setConfirmPassword("");
    setMessage("Sessão encerrada.");
    setPhase("login");
  };

  if (phase === "ready") {
    return (
      <div className="authenticated-shell">
        <div className="admin-session-bar">
          <span><ShieldCheck size={15} /> Administrador: <strong>{currentUser}</strong></span>
          <button type="button" onClick={logout}><LogOut size={15} /> Sair</button>
        </div>
        <App />
      </div>
    );
  }

  const isSetup = phase === "setup";
  const isLoading = phase === "loading";

  return (
    <main className="auth-page">
      <section className="auth-card">
        <div className="auth-brand">
          <div className="auth-mark"><ShieldCheck size={28} /></div>
          <div>
            <span>AstraCalls</span>
            <h1>{isSetup ? "Criar administrador" : "Acesso administrativo"}</h1>
          </div>
        </div>

        <p className="auth-description">
          {isSetup
            ? "Este é o primeiro acesso. Crie o único administrador permitido para este painel."
            : "Entre com a conta administrativa configurada no primeiro acesso."}
        </p>

        <label className="auth-field">
          <span>URL da API</span>
          <div><Server size={17} /><input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="Mesma origem ou http://localhost:8080" disabled={busy} /></div>
        </label>

        {isLoading ? (
          <div className="auth-loading"><LoaderCircle className="spin" size={22} /> Verificando configuração...</div>
        ) : phase === "offline" ? (
          <button className="auth-submit" type="button" onClick={() => void checkAuthentication()}>
            Tentar novamente
          </button>
        ) : (
          <form onSubmit={(event) => void submit(event)}>
            <label className="auth-field">
              <span>Usuário administrador</span>
              <div><UserRound size={17} /><input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required minLength={3} maxLength={64} disabled={busy} /></div>
            </label>
            <label className="auth-field">
              <span>Senha</span>
              <div><KeyRound size={17} /><input type="password" autoComplete={isSetup ? "new-password" : "current-password"} value={password} onChange={(event) => setPassword(event.target.value)} required minLength={12} maxLength={128} disabled={busy} /></div>
            </label>
            {isSetup && (
              <label className="auth-field">
                <span>Confirmar senha</span>
                <div><KeyRound size={17} /><input type="password" autoComplete="new-password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} required minLength={12} maxLength={128} disabled={busy} /></div>
              </label>
            )}
            <button className="auth-submit" type="submit" disabled={busy}>
              {busy ? <LoaderCircle className="spin" size={18} /> : <ShieldCheck size={18} />}
              {isSetup ? "Criar administrador" : "Entrar"}
            </button>
          </form>
        )}

        {message && <div className="auth-message" role="alert">{message}</div>}
        <small className="auth-note">Apenas uma conta administrativa pode existir. Não há cadastro de usuários adicionais.</small>
      </section>
    </main>
  );
};
