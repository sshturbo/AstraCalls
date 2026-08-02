import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CheckCircle2,
  Copy,
  FlaskConical,
  Link2,
  LoaderCircle,
  LogOut,
  MessageSquare,
  Phone,
  Play,
  Plus,
  QrCode,
  Radio,
  RefreshCw,
  Save,
  Server,
  Trash2,
  XCircle,
} from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { apiRequest, eventsUrl } from "./api";
import { apiTools } from "./catalog";
import type { ApiSettings, ApiTool, LogEntry, RequestResult, SessionInfo } from "./types";

const defaultSettings: ApiSettings = { baseUrl: "", apiKey: "" };

const readStoredSettings = (): ApiSettings => {
  try {
    const raw = localStorage.getItem("astracalls-manager-v2-settings");
    if (!raw) return defaultSettings;
    const parsed = JSON.parse(raw) as Partial<ApiSettings>;
    return {
      baseUrl: typeof parsed.baseUrl === "string" ? parsed.baseUrl : "",
      apiKey: typeof parsed.apiKey === "string" ? parsed.apiKey : "",
    };
  } catch {
    return defaultSettings;
  }
};

const formatJSON = (value: unknown) => JSON.stringify(value, null, 2);

const statusLabel = (state: string) => {
  if (state === "open") return "Conectada";
  if (state === "qr") return "Aguardando QR";
  if (state === "connecting") return "Conectando";
  if (state === "logged_out") return "Desconectada";
  return state || "Desconhecido";
};

export const App = () => {
  const initialSettings = useMemo(readStoredSettings, []);
  const [settings, setSettings] = useState<ApiSettings>(initialSettings);
  const [draftSettings, setDraftSettings] = useState<ApiSettings>(initialSettings);
  const [streamState, setStreamState] = useState<"connecting" | "online" | "offline">("connecting");
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [activeId, setActiveId] = useState("");
  const [qrBySession, setQrBySession] = useState<Record<string, string>>({});
  const [newSessionName, setNewSessionName] = useState("");
  const [selectedToolId, setSelectedToolId] = useState(apiTools[0].id);
  const [requestBody, setRequestBody] = useState(formatJSON(apiTools[0].sample ?? {}));
  const [requestResult, setRequestResult] = useState<RequestResult | null>(null);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [busy, setBusy] = useState(false);

  const activeSession = sessions.find((session) => session.id === activeId) ?? null;
  const selectedTool = apiTools.find((tool) => tool.id === selectedToolId) ?? apiTools[0];

  const groupedTools = useMemo(() => {
    const groups = new Map<string, ApiTool[]>();
    for (const tool of apiTools) {
      const current = groups.get(tool.group) ?? [];
      current.push(tool);
      groups.set(tool.group, current);
    }
    return [...groups.entries()];
  }, []);

  const addLog = useCallback((kind: LogEntry["kind"], title: string, payload?: unknown) => {
    const entry: LogEntry = {
      id: crypto.randomUUID(),
      at: new Date().toLocaleTimeString("pt-BR"),
      kind,
      title,
      payload,
    };
    setLogs((current) => [entry, ...current].slice(0, 100));
  }, []);

  const loadSessions = useCallback(async () => {
    const result = await apiRequest(settings, "GET", "/api/sessions");
    if (!result.ok) {
      addLog("error", `Falha ao carregar sessões (${result.status || "rede"})`, result.data);
      return;
    }

    const data = result.data as { sessions?: SessionInfo[] };
    const nextSessions = Array.isArray(data.sessions) ? data.sessions : [];
    setSessions(nextSessions);
    setActiveId((current) => {
      if (current && nextSessions.some((session) => session.id === current)) return current;
      return nextSessions[0]?.id ?? "";
    });
    addLog("success", `${nextSessions.length} sessão(ões) carregada(s)`);
  }, [addLog, settings]);

  useEffect(() => {
    void loadSessions();
  }, [loadSessions]);

  useEffect(() => {
    setStreamState("connecting");
    const source = new EventSource(eventsUrl(settings));

    source.onopen = () => {
      setStreamState("online");
      addLog("success", "Canal de eventos conectado");
    };

    source.onerror = () => {
      setStreamState("offline");
    };

    source.onmessage = (message) => {
      try {
        const event = JSON.parse(message.data) as Record<string, unknown>;
        const type = typeof event.type === "string" ? event.type : "event";
        addLog("event", type, event);

        if (type === "session-list" && Array.isArray(event.sessions)) {
          const nextSessions = event.sessions as SessionInfo[];
          setSessions(nextSessions);
          setActiveId((current) => {
            if (current && nextSessions.some((session) => session.id === current)) return current;
            return nextSessions[0]?.id ?? "";
          });
        }

        if (type === "session-qr" && typeof event.sessionId === "string" && typeof event.qr === "string") {
          setQrBySession((current) => ({ ...current, [event.sessionId as string]: event.qr as string }));
        }

        if (type === "auth-state" && typeof event.sessionId === "string") {
          const sessionId = event.sessionId;
          setSessions((current) =>
            current.map((session) =>
              session.id === sessionId
                ? {
                    ...session,
                    state: typeof event.state === "string" ? event.state : session.state,
                    paired: typeof event.paired === "boolean" ? event.paired : session.paired,
                  }
                : session,
            ),
          );
          if (typeof event.qr === "string" && event.qr) {
            setQrBySession((current) => ({ ...current, [sessionId]: event.qr as string }));
          }
        }
      } catch (error) {
        addLog("error", "Evento SSE inválido", error instanceof Error ? error.message : String(error));
      }
    };

    return () => source.close();
  }, [addLog, settings]);

  const applySettings = () => {
    const next = {
      baseUrl: draftSettings.baseUrl.trim().replace(/\/+$/, ""),
      apiKey: draftSettings.apiKey.trim(),
    };
    localStorage.setItem("astracalls-manager-v2-settings", JSON.stringify(next));
    setSettings(next);
    addLog("info", "Configuração da API atualizada", { baseUrl: next.baseUrl || "mesma origem" });
  };

  const createSession = async () => {
    setBusy(true);
    const result = await apiRequest(settings, "POST", "/api/sessions", {
      name: newSessionName.trim() || "Session",
    });
    setBusy(false);
    if (!result.ok) {
      addLog("error", "Falha ao criar sessão", result.data);
      return;
    }
    const data = result.data as { id?: string };
    if (data.id) setActiveId(data.id);
    setNewSessionName("");
    addLog("success", "Sessão criada", result.data);
    await loadSessions();
  };

  const sessionAction = async (method: "POST" | "DELETE", path: string, title: string) => {
    setBusy(true);
    const result = await apiRequest(settings, method, path);
    setBusy(false);
    addLog(result.ok ? "success" : "error", title, result.data);
    await loadSessions();
  };

  const chooseTool = (tool: ApiTool) => {
    setSelectedToolId(tool.id);
    setRequestBody(formatJSON(tool.sample ?? {}));
    setRequestResult(null);
  };

  const executeTool = async () => {
    if (selectedTool.availability === "planned") return;
    if (selectedTool.path.includes("{sid}") && !activeSession) {
      addLog("error", "Selecione uma sessão antes de executar o teste");
      return;
    }

    let body: unknown = undefined;
    if (selectedTool.method === "POST") {
      try {
        body = requestBody.trim() ? (JSON.parse(requestBody) as unknown) : {};
      } catch (error) {
        const data = { error: error instanceof Error ? error.message : String(error) };
        setRequestResult({ ok: false, status: 0, statusText: "JSON inválido", durationMs: 0, data });
        addLog("error", "O corpo da requisição não contém JSON válido", data);
        return;
      }
    }

    const path = selectedTool.path.replaceAll("{sid}", activeSession?.id ?? "");
    setBusy(true);
    const result = await apiRequest(settings, selectedTool.method, path, body);
    setBusy(false);
    setRequestResult(result);
    addLog(result.ok ? "success" : "error", `${selectedTool.method} ${path}`, result.data);
  };

  const copyResult = async () => {
    if (!requestResult) return;
    await navigator.clipboard.writeText(formatJSON(requestResult.data));
    addLog("info", "Resposta copiada para a área de transferência");
  };

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark"><FlaskConical size={22} /></div>
          <div>
            <strong>AstraCalls Manager v2</strong>
            <span>Conexão, sessões e laboratório da API</span>
          </div>
        </div>
        <div className={`stream-status ${streamState}`}>
          <Radio size={15} />
          {streamState === "online" ? "Eventos online" : streamState === "connecting" ? "Conectando" : "Eventos offline"}
        </div>
      </header>

      <section className="connection-bar panel">
        <label>
          <span>URL da API</span>
          <div className="input-with-icon"><Server size={16} /><input value={draftSettings.baseUrl} onChange={(event) => setDraftSettings((current) => ({ ...current, baseUrl: event.target.value }))} placeholder="Mesma origem ou http://localhost:8080" /></div>
        </label>
        <label>
          <span>API key</span>
          <div className="input-with-icon"><Link2 size={16} /><input type="password" value={draftSettings.apiKey} onChange={(event) => setDraftSettings((current) => ({ ...current, apiKey: event.target.value }))} placeholder="WACALLS_API_KEY" /></div>
        </label>
        <button className="button primary" onClick={applySettings}><Save size={16} />Salvar e conectar</button>
        <button className="button" onClick={() => void loadSessions()}><RefreshCw size={16} />Atualizar</button>
      </section>

      <main className="workspace">
        <aside className="panel sessions-panel">
          <div className="panel-heading">
            <div><span className="eyebrow">Instâncias</span><h2>Sessões</h2></div>
            <span className="counter">{sessions.length}</span>
          </div>

          <div className="create-session">
            <input value={newSessionName} onChange={(event) => setNewSessionName(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void createSession(); }} placeholder="Nome da nova sessão" />
            <button className="icon-button" onClick={() => void createSession()} disabled={busy} title="Criar sessão"><Plus size={18} /></button>
          </div>

          <div className="session-list">
            {sessions.length === 0 ? <div className="empty-mini">Nenhuma sessão cadastrada.</div> : sessions.map((session) => (
              <button key={session.id} className={`session-item ${session.id === activeId ? "active" : ""}`} onClick={() => setActiveId(session.id)}>
                <span className={`status-dot ${session.paired ? "ok" : "warn"}`} />
                <span className="session-copy"><strong>{session.name}</strong><small>{session.jid || statusLabel(session.state)}</small></span>
              </button>
            ))}
          </div>

          {activeSession && (
            <div className="session-detail">
              <div className="session-detail-head">
                <div><span className="eyebrow">Selecionada</span><h3>{activeSession.name}</h3></div>
                <span className={`badge ${activeSession.paired ? "success" : "warning"}`}>{statusLabel(activeSession.state)}</span>
              </div>

              {!activeSession.paired && qrBySession[activeSession.id] ? (
                <div className="qr-box"><QRCodeSVG value={qrBySession[activeSession.id]} size={176} level="M" /><span>Escaneie em Aparelhos conectados</span></div>
              ) : !activeSession.paired ? (
                <div className="qr-placeholder"><QrCode size={34} /><span>Aguardando geração do QR</span></div>
              ) : (
                <div className="connected-box"><CheckCircle2 size={28} /><div><strong>Sessão conectada</strong><span>{activeSession.jid}</span></div></div>
              )}

              <div className="session-actions">
                {!activeSession.paired && <button className="button" onClick={() => void sessionAction("POST", `/api/sessions/${activeSession.id}/pair`, "Novo pareamento solicitado")}><QrCode size={15} />Gerar QR</button>}
                {activeSession.paired && <button className="button" onClick={() => void sessionAction("POST", `/api/sessions/${activeSession.id}/logout`, "Sessão desconectada")}><LogOut size={15} />Desconectar</button>}
                <button className="button danger" onClick={() => { if (window.confirm(`Excluir a sessão ${activeSession.name}?`)) void sessionAction("DELETE", `/api/sessions/${activeSession.id}`, "Sessão excluída"); }}><Trash2 size={15} />Excluir</button>
              </div>
            </div>
          )}
        </aside>

        <section className="panel lab-panel">
          <div className="panel-heading">
            <div><span className="eyebrow">Postman especializado</span><h2>Laboratório da API</h2></div>
            <FlaskConical size={22} />
          </div>

          <div className="lab-layout">
            <nav className="tool-nav">
              {groupedTools.map(([group, tools]) => (
                <div className="tool-group" key={group}>
                  <span>{group}</span>
                  {tools.map((tool) => (
                    <button key={tool.id} disabled={tool.availability === "planned"} className={tool.id === selectedTool.id ? "active" : ""} onClick={() => chooseTool(tool)} title={tool.availability === "planned" ? "Será liberado quando o endpoint existir no backend" : tool.description}>
                      {tool.group === "Chamadas" ? <Phone size={15} /> : <MessageSquare size={15} />}
                      <span>{tool.label}</span>
                      {tool.availability === "planned" && <small>em breve</small>}
                    </button>
                  ))}
                </div>
              ))}
            </nav>

            <div className="request-editor">
              <div className="endpoint-line">
                <span className={`method ${selectedTool.method.toLowerCase()}`}>{selectedTool.method}</span>
                <code>{selectedTool.path.replaceAll("{sid}", activeSession?.id ?? "{sid}")}</code>
              </div>
              <p>{selectedTool.description}</p>

              {selectedTool.method === "POST" && (
                <label className="json-editor-label">
                  <span>Corpo JSON</span>
                  <textarea value={requestBody} onChange={(event) => setRequestBody(event.target.value)} spellCheck={false} />
                </label>
              )}

              <button className="button primary run-button" onClick={() => void executeTool()} disabled={busy || selectedTool.availability === "planned"}>
                {busy ? <LoaderCircle className="spin" size={17} /> : <Play size={17} />}
                {selectedTool.availability === "planned" ? "Endpoint ainda não implementado" : "Executar teste"}
              </button>
            </div>
          </div>
        </section>

        <aside className="right-column">
          <section className="panel response-panel">
            <div className="panel-heading compact">
              <div><span className="eyebrow">Resultado</span><h2>Resposta</h2></div>
              {requestResult && <button className="icon-button" onClick={() => void copyResult()} title="Copiar resposta"><Copy size={16} /></button>}
            </div>
            {requestResult ? (
              <>
                <div className="response-meta">
                  <span className={requestResult.ok ? "ok-text" : "error-text"}>{requestResult.ok ? <CheckCircle2 size={15} /> : <XCircle size={15} />}{requestResult.status || "erro"} {requestResult.statusText}</span>
                  <span>{requestResult.durationMs} ms</span>
                </div>
                <pre>{formatJSON(requestResult.data)}</pre>
              </>
            ) : <div className="empty-mini">Execute uma operação para ver status, duração e JSON retornado.</div>}
          </section>

          <section className="panel logs-panel">
            <div className="panel-heading compact">
              <div><span className="eyebrow">Depuração</span><h2>Eventos e logs</h2></div>
              <button className="text-button" onClick={() => setLogs([])}>Limpar</button>
            </div>
            <div className="log-list">
              {logs.length === 0 ? <div className="empty-mini">Os eventos SSE e resultados das requisições aparecerão aqui.</div> : logs.map((entry) => (
                <details className={`log-entry ${entry.kind}`} key={entry.id}>
                  <summary><span>{entry.at}</span><strong>{entry.title}</strong></summary>
                  {entry.payload !== undefined && <pre>{formatJSON(entry.payload)}</pre>}
                </details>
              ))}
            </div>
          </section>
        </aside>
      </main>
    </div>
  );
};
