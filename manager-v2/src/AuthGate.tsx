import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import {
  Copy,
  Eye,
  EyeOff,
  KeyRound,
  LoaderCircle,
  LogOut,
  Save,
  Server,
  ShieldCheck,
  UserRound,
  X,
} from "lucide-react";
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

type ProfileData = {
  username?: string;
  role?: string;
  expiresAt?: number;
  generalToken?: string;
  generalTokenBy?: string;
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

  const [profileOpen, setProfileOpen] = useState(false);
  const [profileLoading, setProfileLoading] = useState(false);
  const [profileBusy, setProfileBusy] = useState(false);
  const [profileUsername, setProfileUsername] = useState("");
  const [generalToken, setGeneralToken] = useState("");
  const [generalTokenBy, setGeneralTokenBy] = useState("");
  const [tokenVisible, setTokenVisible] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmNewPassword, setConfirmNewPassword] = useState("");
  const [profileMessage, setProfileMessage] = useState("");
  const [profileSuccess, setProfileSuccess] = useState(false);

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
      setProfileOpen(false);
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
    setProfileOpen(false);
    setPassword("");
    setConfirmPassword("");
    setMessage("Sessão encerrada.");
    setPhase("login");
  };

  const openProfile = async () => {
    setProfileOpen(true);
    setProfileLoading(true);
    setProfileMessage("");
    setProfileSuccess(false);
    setTokenVisible(false);
    setCurrentPassword("");
    setNewPassword("");
    setConfirmNewPassword("");

    const result = await apiRequest(settingsFor(baseUrl), "GET", "/api/auth/profile");
    setProfileLoading(false);
    const data = (result.data ?? {}) as ProfileData;
    if (!result.ok) {
      setProfileMessage(data.error || `Falha ao carregar o perfil (${result.status || "rede"}).`);
      return;
    }
    setProfileUsername(data.username ?? currentUser);
    setGeneralToken(data.generalToken ?? "");
    setGeneralTokenBy(data.generalTokenBy ?? "");
  };

  const closeProfile = () => {
    if (profileBusy) return;
    setProfileOpen(false);
    setTokenVisible(false);
    setCurrentPassword("");
    setNewPassword("");
    setConfirmNewPassword("");
    setProfileMessage("");
    setProfileSuccess(false);
  };

  const copyGeneralToken = async () => {
    if (!generalToken) return;
    try {
      await navigator.clipboard.writeText(generalToken);
      setProfileSuccess(true);
      setProfileMessage("Token geral copiado para a área de transferência.");
    } catch {
      setProfileSuccess(false);
      setProfileMessage("O navegador não permitiu copiar o token automaticamente.");
    }
  };

  const saveProfile = async (event: FormEvent) => {
    event.preventDefault();
    setProfileMessage("");
    setProfileSuccess(false);

    const normalizedUsername = profileUsername.trim();
    if (normalizedUsername.length < 3) {
      setProfileMessage("O nome de acesso deve ter pelo menos 3 caracteres.");
      return;
    }
    if (!currentPassword) {
      setProfileMessage("Informe a senha atual para salvar as alterações.");
      return;
    }
    if (newPassword && newPassword.length < 12) {
      setProfileMessage("A nova senha deve ter pelo menos 12 caracteres.");
      return;
    }
    if (newPassword !== confirmNewPassword) {
      setProfileMessage("A confirmação da nova senha não corresponde.");
      return;
    }
    if (normalizedUsername.toLowerCase() === currentUser.toLowerCase() && !newPassword) {
      setProfileMessage("Altere o nome de acesso ou informe uma nova senha.");
      return;
    }

    setProfileBusy(true);
    const result = await apiRequest(settingsFor(baseUrl), "POST", "/api/auth/profile", {
      username: normalizedUsername,
      currentPassword,
      newPassword,
    });
    setProfileBusy(false);

    const data = (result.data ?? {}) as AuthResponse;
    if (!result.ok || !data.token) {
      setProfileMessage(data.error || `Falha ao atualizar o perfil (${result.status || "rede"}).`);
      return;
    }

    sessionStorage.setItem(AUTH_TOKEN_STORAGE_KEY, data.token);
    const updatedUsername = data.user?.username ?? normalizedUsername.toLowerCase();
    setCurrentUser(updatedUsername);
    setProfileUsername(updatedUsername);
    setCurrentPassword("");
    setNewPassword("");
    setConfirmNewPassword("");
    setProfileSuccess(true);
    setProfileMessage("Perfil atualizado. Os tokens administrativos anteriores foram invalidados.");
  };

  if (phase === "ready") {
    return (
      <div className="authenticated-shell">
        <div className="admin-session-bar">
          <span><ShieldCheck size={15} /> Administrador: <strong>{currentUser}</strong></span>
          <div className="admin-session-actions">
            <button className="profile-trigger" type="button" onClick={() => void openProfile()}>
              <UserRound size={15} /> Perfil
            </button>
            <button className="logout-trigger" type="button" onClick={logout}><LogOut size={15} /> Sair</button>
          </div>
        </div>
        <App />

        {profileOpen && (
          <div className="profile-overlay" role="presentation" onMouseDown={(event) => {
            if (event.target === event.currentTarget) closeProfile();
          }}>
            <section className="profile-dialog" role="dialog" aria-modal="true" aria-labelledby="profile-title">
              <div className="profile-header">
                <div>
                  <span>Administrador único</span>
                  <h2 id="profile-title">Perfil e credenciais</h2>
                </div>
                <button type="button" onClick={closeProfile} disabled={profileBusy} aria-label="Fechar perfil"><X size={19} /></button>
              </div>

              {profileLoading ? (
                <div className="profile-loading"><LoaderCircle className="spin" size={22} /> Carregando perfil...</div>
              ) : (
                <>
                  <div className="profile-token-card">
                    <div className="profile-token-heading">
                      <div>
                        <strong>Token geral da API</strong>
                        <span>{generalTokenBy === "environment" ? "Definido por WACALLS_API_KEY" : "Gerado e persistido no PostgreSQL"}</span>
                      </div>
                      <ShieldCheck size={20} />
                    </div>
                    <div className="profile-token-value">
                      <input type={tokenVisible ? "text" : "password"} value={generalToken} readOnly aria-label="Token geral da API" />
                      <button type="button" onClick={() => setTokenVisible((visible) => !visible)} title={tokenVisible ? "Ocultar token" : "Mostrar token"}>
                        {tokenVisible ? <EyeOff size={17} /> : <Eye size={17} />}
                      </button>
                      <button type="button" onClick={() => void copyGeneralToken()} title="Copiar token" disabled={!generalToken}>
                        <Copy size={17} />
                      </button>
                    </div>
                    <small>Este token concede acesso completo à API por meio do header <code>X-API-Key</code>. Não compartilhe publicamente.</small>
                  </div>

                  <form className="profile-form" onSubmit={(event) => void saveProfile(event)}>
                    <label className="auth-field">
                      <span>Nome de acesso</span>
                      <div><UserRound size={17} /><input autoComplete="username" value={profileUsername} onChange={(event) => setProfileUsername(event.target.value)} minLength={3} maxLength={64} disabled={profileBusy} /></div>
                    </label>

                    <div className="profile-password-grid">
                      <label className="auth-field">
                        <span>Senha atual</span>
                        <div><KeyRound size={17} /><input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} maxLength={128} disabled={profileBusy} /></div>
                      </label>
                      <label className="auth-field">
                        <span>Nova senha</span>
                        <div><KeyRound size={17} /><input type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} minLength={12} maxLength={128} placeholder="Deixe vazio para manter" disabled={profileBusy} /></div>
                      </label>
                      <label className="auth-field profile-confirm-password">
                        <span>Confirmar nova senha</span>
                        <div><KeyRound size={17} /><input type="password" autoComplete="new-password" value={confirmNewPassword} onChange={(event) => setConfirmNewPassword(event.target.value)} minLength={12} maxLength={128} placeholder="Repita a nova senha" disabled={profileBusy} /></div>
                      </label>
                    </div>

                    <button className="profile-save" type="submit" disabled={profileBusy}>
                      {profileBusy ? <LoaderCircle className="spin" size={18} /> : <Save size={18} />}
                      Salvar alterações
                    </button>
                  </form>
                </>
              )}

              {profileMessage && <div className={`profile-message ${profileSuccess ? "success" : "error"}`} role="status">{profileMessage}</div>}
            </section>
          </div>
        )}
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
