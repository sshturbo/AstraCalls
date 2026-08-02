import type { ApiSettings, HttpMethod, RequestResult } from "./types";

const trimTrailingSlash = (value: string) => value.trim().replace(/\/+$/, "");

export const apiUrl = (settings: ApiSettings, path: string) => {
  const base = trimTrailingSlash(settings.baseUrl);
  return `${base}${path}`;
};

export const eventsUrl = (settings: ApiSettings) => {
  const url = new URL(apiUrl(settings, "/api/events"), window.location.origin);
  if (settings.apiKey.trim()) {
    url.searchParams.set("apiKey", settings.apiKey.trim());
  }
  url.searchParams.set("clientId", "manager-v2");
  return url.toString();
};

export const apiRequest = async (
  settings: ApiSettings,
  method: HttpMethod,
  path: string,
  body?: unknown,
): Promise<RequestResult> => {
  const startedAt = performance.now();
  const headers = new Headers({ Accept: "application/json" });
  if (settings.apiKey.trim()) {
    headers.set("X-API-Key", settings.apiKey.trim());
  }

  const init: RequestInit = { method, headers };
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
    return {
      ok: false,
      status: 0,
      statusText: "Network error",
      durationMs: Math.round(performance.now() - startedAt),
      data: { error: error instanceof Error ? error.message : String(error) },
    };
  }
};
