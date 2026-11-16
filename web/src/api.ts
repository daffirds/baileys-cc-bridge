// The JSON API served by `bailey-cc-bridge ui`. Every call carries the token from the link (#t=…).

export type Agent = {
  engine?: string;
  model?: string;
  effort?: string;
  autonomy?: string;
  workdir?: string;
  prompt?: string;
  idle_timeout?: string;
};

export type Chat = Agent & { jid: string; name?: string };

export type Config = { defaults: Agent; chats: Chat[] };

export type Group = { jid: string; name: string; size: number };

export type State = {
  config_path: string;
  chats_dir: string; // each chat's default workdir lives under here
  linked: string; // phone number of the linked account, "" if not linked
  me: string; // own JID ("Message yourself"), "" if unknown
  config: Config;
  groups: Group[];
  groups_updated: string; // RFC 3339, "" if never fetched
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
  state: () => call<State>('GET', '/api/state'),
  save: (config: Config) => call<{ ok: boolean }>('PUT', '/api/config', config),
  refreshGroups: () => call<{ groups: Group[]; groups_updated: string }>('POST', '/api/groups'),
  pickDir: (start: string) => call<{ path: string }>('POST', '/api/pick-dir', { start }),
  checkDir: (path: string) => call<{ ok: boolean; path: string; error?: string }>('POST', '/api/check-dir', { path }),
};
