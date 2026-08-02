import type { ApiSettings, HttpMethod, RequestResult } from "./types";

const REQUEST_TIMEOUT_MS = 60_000;
const STREAM_RECONNECT_MS = 2_000;

const trimTrailingSlash = (value: string) => value.trim().replace(/\/+$/, "");

export const apiUrl = (settings: ApiSettings, path: string) => {
  const base = trimTrailingSlash(settings.baseUrl);
  return `${base}${path}`;
};

const authenticatedHeaders = (settings: ApiSettings, accept: string) => {
  const headers = new Headers({ Accept: accept });
  if (settings.apiKey.trim()) {
    headers.set("X-API-Key", settings.apiKey.trim());
  }
  return headers;
};

const eventStreamUrl = (settings: ApiSettings) => {
  const url = new URL(apiUrl(settings, "/api/events"), window.location.origin);
  url.searchParams.set("clientId", "manager-v2");
  return url.toString();
};

type EventStreamHandlers = {
  onOpen: () => void;
  onMessage: (data: string) => void;
  onError: (error: Error) => void;
  onConnecting?: () => void;
};

export const subscribeToEvents = (settings: ApiSettings, handlers: EventStreamHandlers) => {
  const controller = new AbortController();
  let reconnectTimer: number | undefined;
  let stopped = false;

  const connect = async () => {
    handlers.onConnecting?.();

    try {
      const response = await fetch(eventStreamUrl(settings), {
        method: "GET",
        headers: authenticatedHeaders(settings, "text/event-stream"),
        cache: "no-store",
        signal: controller.signal,
      });

      if (!response.ok) {
        throw new Error(`SSE HTTP ${response.status} ${response.statusText}`.trim());
      }
      if (!response.body) {
        throw new Error("O navegador não disponibilizou o corpo do stream SSE");
      }

      handlers.onOpen();
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let dataLines: string[] = [];

      const dispatch = () => {
        if (dataLines.length > 0) {
          handlers.onMessage(dataLines.join("\n"));
          dataLines = [];
        }
      };

      while (!stopped) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        let newlineIndex = buffer.indexOf("\n");
        while (newlineIndex >= 0) {
          let line = buffer.slice(0, newlineIndex);
          buffer = buffer.slice(newlineIndex + 1);
          if (line.endsWith("\r")) line = line.slice(0, -1);

          if (line === "") {
            dispatch();
          } else if (line.startsWith("data:")) {
            dataLines.push(line.slice(5).trimStart());
          }
          newlineIndex = buffer.indexOf("\n");
        }
      }

      buffer += decoder.decode();
      if (buffer.startsWith("data:")) {
        dataLines.push(buffer.slice(5).trimStart());
      }
      dispatch();

      if (!stopped) {
        throw new Error("Canal de eventos encerrado pelo servidor");
      }
    } catch (error) {
      if (stopped || (error instanceof DOMException && error.name === "AbortError")) return;
      handlers.onError(error instanceof Error ? error : new Error(String(error)));
    }

    if (!stopped) {
      reconnectTimer = window.setTimeout(() => void connect(), STREAM_RECONNECT_MS);
    }
  };

  void connect();

  return () => {
    stopped = true;
    controller.abort();
    if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer);
  };
};

export const apiRequest = async (
  settings: ApiSettings,
  method: HttpMethod,
  path: string,
  body?: unknown,
): Promise<RequestResult> => {
  const startedAt = performance.now();
  const headers = authenticatedHeaders(settings, "application/json");
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);

  const init: RequestInit = { method, headers, signal: controller.signal };
  if (body !== undefined && method !== "GET" && method !== "DELETE") {
    headers.set("Content-Type", "application/json");
    init.body = JSON.stringify(body);
  }

  try {
    const response = await fetch(apiUrl(settings, path), init);
    const text = await response.text();
    let data: unknown = null;
    if (text) {
      try {
        data = JSON.parse(text) as unknown;
      } catch {
        data = text;
      }
    }

    return {
      ok: response.ok,
      status: response.status,
      statusText: response.statusText,
      durationMs: Math.round(performance.now() - startedAt),
      data,
    };
  } catch (error) {
    const timedOut = error instanceof DOMException && error.name === "AbortError";
    return {
      ok: false,
      status: 0,
      statusText: timedOut ? "Request timeout" : "Network error",
      durationMs: Math.round(performance.now() - startedAt),
      data: {
        error: timedOut
          ? `A requisição excedeu ${REQUEST_TIMEOUT_MS / 1000} segundos`
          : error instanceof Error
            ? error.message
            : String(error),
      },
    };
  } finally {
    window.clearTimeout(timeout);
  }
};
