export type HttpMethod = "GET" | "POST" | "DELETE";

export type SessionInfo = {
  id: string;
  name: string;
  jid: string;
  state: string;
  paired: boolean;
};

export type ApiSettings = {
  baseUrl: string;
  apiKey: string;
};

export type RequestResult = {
  ok: boolean;
  status: number;
  statusText: string;
  durationMs: number;
  data: unknown;
};

export type LogEntry = {
  id: string;
  at: string;
  kind: "info" | "success" | "error" | "event";
  title: string;
  payload?: unknown;
};

export type ApiTool = {
  id: string;
  group: string;
  label: string;
  method: HttpMethod;
  path: string;
  description: string;
  sample?: unknown;
  availability: "current" | "planned";
};
