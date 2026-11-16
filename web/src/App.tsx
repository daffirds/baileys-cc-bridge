import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, type Agent, type Chat, type Config, type Group, type State } from './api';

const ENGINES = ['claude', 'codex'];
const AUTONOMY: Record<string, string> = {
  auto: 'auto: the engine’s own reviewer decides',
  full: 'full: no prompts, no sandbox',
  readonly: 'readonly: read-only, the rest is denied',
};

const message = (e: unknown) => (e instanceof Error ? e.message : String(e));

// Drops empty fields so the saved YAML only holds what was set.
function clean<T extends object>(o: T): T {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== '' && v !== undefined)) as T;
}

// Mirrors the Go side: the directory name a chat's files live under.
const dirName = (jid: string) => jid.replace(/[^A-Za-z0-9.-]/g, '_');

export function App() {
  const [state, setState] = useState<State | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [saved, setSaved] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.state().then(
      (s) => {
        const c = { defaults: s.config.defaults ?? {}, chats: s.config.chats ?? [] };
        setState(s);
        setConfig(c);
        setSaved(JSON.stringify(c));
      },
      (e) => setError(message(e)),
    );
  }, []);

  if (!state || !config) {
    return <main className="page">{error ? <p className="error">{error}</p> : <p className="muted">Loading…</p>}</main>;
  }

  const dirty = JSON.stringify(config) !== saved;
  const save = async () => {
    setBusy(true);
    setError('');
    try {
      await api.save(config);
      setSaved(JSON.stringify(config));
      setNotice('Saved. A running bridge picks it up within a few seconds.');
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const setChat = (i: number, c: Chat) => setConfig({ ...config, chats: config.chats.map((x, j) => (j === i ? c : x)) });

  return (
    <main className="page">
      <header className="top">
        <div>
          <h1>bailey-cc-bridge</h1>
          <p className="muted">
            {state.linked ? (
              <>
                WhatsApp <b>+{state.linked}</b>
              </>
            ) : (
              <span className="warn">
                Not linked: run <code>bailey-cc-bridge login</code>
              </span>
            )}{' '}
            · <code>{state.config_path}</code>
          </p>
        </div>
        <button className="primary" disabled={!dirty || busy} onClick={save}>
          {busy ? 'Saving…' : dirty ? 'Save' : 'Saved'}
        </button>
      </header>
      {error && <p className="error">{error}</p>}
      {notice && !dirty && <p className="notice">{notice}</p>}

      <section className="card">
        <h2>Defaults</h2>
        <AgentFields value={config.defaults} onChange={(d) => setConfig({ ...config, defaults: d })} />
      </section>

      <h2 className="section">Chats</h2>
      <p className="muted">Every message in these chats goes to that chat’s own agent session. Everything else is ignored.</p>
      {config.chats.map((c, i) => (
        <ChatCard
          key={c.jid}
          chat={c}
          defaults={config.defaults}
          state={state}
          onChange={(c) => setChat(i, c)}
          onRemove={() => setConfig({ ...config, chats: config.chats.filter((_, j) => j !== i) })}
        />
      ))}
      <AddChat
        state={state}
        existing={config.chats}
        onAdd={(c) => setConfig({ ...config, chats: [...config.chats, c] })}
        onGroups={(groups, updated) => setState({ ...state, groups, groups_updated: updated })}
      />
    </main>
  );
}

function Field({ label, wide, children }: { label: string; wide?: boolean; children: ReactNode }) {
  return (
    <label className={wide ? 'field wide' : 'field'}>
      <span>{label}</span>
      {children}
    </label>
  );
}

// AgentFields edits the defaults (no `defaults` given) or a chat's overrides of them.
function AgentFields<T extends Agent>({ value, onChange, defaults, defaultDir }: {
  value: T;
  onChange: (v: T) => void;
  defaults?: Agent;
  defaultDir?: string;
}) {
  const set = (patch: Partial<Agent>) => onChange(clean({ ...value, ...patch }));
  const inherited = (v: string | undefined, fallback: string) => `default (${v || fallback})`;
  return (
    <div className="grid">
      <Field label="Engine">
        <select value={defaults ? (value.engine ?? '') : value.engine || 'claude'} onChange={(e) => set({ engine: e.target.value })}>
          {defaults && <option value="">{inherited(defaults.engine, 'claude')}</option>}
          {ENGINES.map((e) => (
            <option key={e} value={e}>
              {e}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Autonomy">
        <select value={defaults ? (value.autonomy ?? '') : value.autonomy || 'auto'} onChange={(e) => set({ autonomy: e.target.value })}>
          {defaults && <option value="">{inherited(defaults.autonomy, 'auto')}</option>}
          {Object.entries(AUTONOMY).map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Model">
        <input
          value={value.model ?? ''}
          placeholder={defaults ? inherited(defaults.model, 'CLI default') : 'CLI default'}
          onChange={(e) => set({ model: e.target.value })}
        />
      </Field>
      <Field label="Effort">
        <input
          value={value.effort ?? ''}
          placeholder={defaults ? inherited(defaults.effort, 'model default') : 'model default'}
          onChange={(e) => set({ effort: e.target.value })}
        />
      </Field>
      <Field label="Idle timeout">
        <input
          value={value.idle_timeout ?? ''}
          placeholder={defaults ? inherited(defaults.idle_timeout, '30m') : '30m'}
          onChange={(e) => set({ idle_timeout: e.target.value })}
        />
      </Field>
      <Field label="Working directory" wide>
        <DirInput
          value={value.workdir ?? ''}
          placeholder={defaults ? defaults.workdir || defaultDir || '' : 'each chat’s own folder'}
          onChange={(workdir) => set({ workdir })}
        />
      </Field>
      <Field label="Prompt" wide>
        <textarea
          rows={3}
          value={value.prompt ?? ''}
          placeholder={defaults ? (defaults.prompt ? 'default prompt' : 'none') : 'appended to every chat’s instructions'}
          onChange={(e) => set({ prompt: e.target.value })}
        />
      </Field>
    </div>
  );
}

function DirInput({ value, placeholder, onChange }: { value: string; placeholder: string; onChange: (v: string) => void }) {
  const [problem, setProblem] = useState('');
  useEffect(() => {
    if (!value) {
      setProblem('');
      return;
    }
    const t = setTimeout(() => {
      api.checkDir(value).then(
        (r) => setProblem(r.ok ? '' : (r.error ?? 'not a directory')),
        () => setProblem(''),
      );
    }, 300);
    return () => clearTimeout(t);
  }, [value]);
  const choose = async () => {
    try {
      const { path } = await api.pickDir(value || placeholder);
      if (path) onChange(path);
    } catch (e) {
      setProblem(message(e));
    }
  };
  return (
    <div className="dir">
      <div className="row">
        <input className="mono" value={value} placeholder={placeholder} spellCheck={false} onChange={(e) => onChange(e.target.value)} />
        <button type="button" onClick={choose}>
          Choose…
        </button>
        {value && (
          <button type="button" className="ghost" title="Use the default" onClick={() => onChange('')}>
            ×
          </button>
        )}
      </div>
      {problem && <span className="error small">{problem}</span>}
    </div>
  );
}

function ChatCard({ chat, defaults, state, onChange, onRemove }: {
  chat: Chat;
  defaults: Agent;
  state: State;
  onChange: (c: Chat) => void;
  onRemove: () => void;
}) {
  const kind = chat.jid.endsWith('@g.us') ? 'group' : chat.jid === state.me ? 'you' : 'person';
  return (
    <section className="card">
      <div className="row head">
        <input className="name" value={chat.name ?? ''} placeholder="name" onChange={(e) => onChange({ ...chat, name: e.target.value })} />
        <span className={`badge ${kind}`}>{kind}</span>
        <code className="muted jid">{chat.jid}</code>
        <button type="button" className="ghost danger" onClick={onRemove}>
          Remove
        </button>
      </div>
      <AgentFields value={chat} defaults={defaults} defaultDir={`${state.chats_dir}/${dirName(chat.jid)}/work`} onChange={onChange} />
    </section>
  );
}

function AddChat({ state, existing, onAdd, onGroups }: {
  state: State;
  existing: Chat[];
  onAdd: (c: Chat) => void;
  onGroups: (groups: Group[], updated: string) => void;
}) {
  const [q, setQ] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const have = useMemo(() => new Set(existing.map((c) => c.jid)), [existing]);
  const needle = q.trim().toLowerCase();
  const digits = q.replace(/[\s+\-().]/g, '');
  const number = /^\d{7,15}$/.test(digits) ? digits : '';
  const groups = useMemo(
    () => state.groups.filter((g) => !have.has(g.jid) && g.name.toLowerCase().includes(needle)).slice(0, 8),
    [state.groups, have, needle],
  );
  const add = (c: Chat) => {
    onAdd(c);
    setQ('');
  };
  const refresh = async () => {
    setBusy(true);
    setErr('');
    try {
      const r = await api.refreshGroups();
      onGroups(r.groups, r.groups_updated);
    } catch (e) {
      setErr(message(e));
    } finally {
      setBusy(false);
    }
  };
  const showMe = state.me && !have.has(state.me) && (!needle || 'you message yourself me'.includes(needle));
  return (
    <section className="card add">
      <h3>Add a chat</h3>
      <input value={q} placeholder="Search your groups, or type a phone number with country code" onChange={(e) => setQ(e.target.value)} />
      <ul className="options">
        {showMe && (
          <li>
            <button type="button" onClick={() => add({ jid: state.me, name: 'Me' })}>
              You <span className="muted">your “Message yourself” chat</span>
            </button>
          </li>
        )}
        {number && !have.has(`${number}@s.whatsapp.net`) && (
          <li>
            <button type="button" onClick={() => add({ jid: `${number}@s.whatsapp.net`, name: `+${number}` })}>
              +{number} <span className="muted">a person</span>
            </button>
          </li>
        )}
        {groups.map((g) => (
          <li key={g.jid}>
            <button type="button" onClick={() => add({ jid: g.jid, name: g.name })}>
              {g.name} <span className="muted">{g.size} members</span>
            </button>
          </li>
        ))}
      </ul>
      <p className="muted small">
        {state.groups.length} groups{state.groups_updated ? `, listed ${new Date(state.groups_updated).toLocaleString()}` : ' listed so far'} ·{' '}
        <button type="button" className="link" disabled={busy} onClick={refresh}>
          {busy ? 'Refreshing…' : 'Refresh from WhatsApp'}
        </button>
      </p>
      {err && <p className="error small">{err}</p>}
    </section>
  );
}
