// The JSON API of the panel, served by `baileys-cc-bridge run`. Every call carries the token from the
// panel's link (#t=…).

export type Agent = {
  engine?: string;
  model?: string;
  effort?: string;
  autonomy?: string;
  workdir?: string;
  system_prompt?: string;
  footer?: boolean; // "Assisted by …" under what the agent sends; on unless false
};

export type Chat = Agent & { jid: string; name?: string };

export type Config = { chats: Chat[] };

export type Group = { jid: string; name: string; size: number };

export type Person = { jid: string; name?: string };

// A model an engine's CLI offers; the first one, id "", is the CLI's own default.
export type Model = { id: string; name: string; description?: string; efforts?: string[]; default_effort?: string };

export type Status = {
  state: 'starting' | 'linking' | 'connected' | 'logging_out';
  me?: { id: string; lid?: string; name?: string };
  number?: string; // the linked number, while connected
  error?: string; // why the last connection failed
  qr?: string; // the code to scan while linking, as an image
};

export type State = {
  chats_dir: string; // each chat's default folder lives under here
  config: Config;
  default_prompts: { group: string; direct: string }; // the system prompt a chat gets unless it sets its own
};

const token = new URLSearchParams(location.hash.slice(1)).get('t') ?? '';

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Bridge-Token': token },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error ?? res.statusText);
  return data as T;
}

export const api = {
  status: () => call<Status>('GET', '/api/status'),
  state: () => call<State>('GET', '/api/state'),
  save: (config: Config) => call<{ ok: boolean }>('PUT', '/api/config', config),
  groups: () => call<{ groups: Group[] }>('GET', '/api/groups'),
  people: () => call<{ people: Person[] }>('GET', '/api/people'),
  logout: () => call<{ ok: boolean }>('POST', '/api/logout'),
  models: (engine: string) => call<{ models: Model[] }>('GET', `/api/models?engine=${encodeURIComponent(engine)}`),
  pickDir: (start: string) => call<{ path: string }>('POST', '/api/pick-dir', { start }),
  checkDir: (path: string) => call<{ ok: boolean; path: string; error?: string }>('POST', '/api/check-dir', { path }),
};
