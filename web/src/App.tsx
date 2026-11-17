import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, type Agent, type Chat, type Config, type Group, type State, type Status } from './api';

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

// App follows where WhatsApp stands: a QR code to scan while no number is linked, the editor once
// one is. A logout or a new link switches the page by itself.
export function App() {
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let alive = true;
    const tick = () =>
      api.status().then(
        (s) => alive && (setStatus(s), setError('')),
        (e) => alive && setError(message(e)),
      );
    tick();
    const t = setInterval(tick, 2000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  if (!status) {
    return <main className="page">{error ? <p className="error">{error}</p> : <p className="muted">Loading…</p>}</main>;
  }
  if (status.state === 'connected' && status.number) {
    return <Panel key={status.number} status={status} />;
  }
  return (
    <main className="page">
      <header className="top">
        <h1>bailey-cc-bridge</h1>
      </header>
      <Linking status={status} />
      {error && <p className="error">{error}</p>}
    </main>
  );
}

function Linking({ status }: { status: Status }) {
  if (status.state === 'logging_out') {
    return <section className="card center muted">Logging out…</section>;
  }
  if (status.state === 'linking' && status.qr) {
    return (
      <section className="card center linking">
        <h2>Link WhatsApp</h2>
        <img className="qr" src={status.qr} alt="QR code to link WhatsApp" />
        <ol className="steps">
          <li>Open WhatsApp on the phone with the number to use</li>
          <li>
            Settings → <b>Linked devices</b> → <b>Link a device</b>
          </li>
          <li>Point the phone at this code. It refreshes by itself.</li>
        </ol>
        <p className="muted small">The agents will read and write as that number.</p>
      </section>
    );
  }
  return (
    <section className="card center">
      <p className="muted">Connecting to WhatsApp…</p>
      {status.error && <p className="error small">{status.error}</p>}
    </section>
  );
}

function Panel({ status }: { status: Status }) {
  const [state, setState] = useState<State | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [saved, setSaved] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [leaving, setLeaving] = useState(false);

  useEffect(() => {
    api.state().then(
      (s) => {
        const c = { chats: s.config.chats ?? [] };
        setState(s);
        setConfig(c);
        setSaved(JSON.stringify(c));
      },
      (e) => setError(message(e)),
    );
  }, []);

  const me = status.me?.id ?? '';
  const who = status.me?.name ? `${status.me.name} · +${status.number}` : `+${status.number}`;
  const dirty = config !== null && JSON.stringify(config) !== saved;
  const save = async () => {
    if (!config) return;
    setBusy(true);
    setError('');
    try {
      await api.save(config);
      setSaved(JSON.stringify(config));
      setNotice('Saved. The bridge picks it up within a few seconds.');
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const setChat = (i: number, c: Chat) => config && setConfig({ chats: config.chats.map((x, j) => (j === i ? c : x)) });

  return (
    <main className="page">
      <header className="top">
        <div>
          <h1>bailey-cc-bridge</h1>
          <p className="muted">
            WhatsApp <b>{who}</b>
            {state && (
              <>
                {' '}
                · <code>{state.config_path}</code>
              </>
            )}
          </p>
        </div>
        <div className="row">
          <button type="button" className="ghost danger" onClick={() => setLeaving(true)}>
            Log out
          </button>
          <button className="primary" disabled={!dirty || busy} onClick={save}>
            {busy ? 'Saving…' : dirty ? 'Save' : 'Saved'}
          </button>
        </div>
      </header>
      {error && <p className="error">{error}</p>}
      {notice && !dirty && <p className="notice">{notice}</p>}
      {leaving && <LogoutModal who={who} onClose={() => setLeaving(false)} />}

      {state && config && (
        <>
          <h2 className="section">Chats</h2>
          <p className="muted">Every message in these chats goes to that chat’s own agent session. Everything else is ignored.</p>
          {config.chats.map((c, i) => (
            <ChatCard
              key={c.jid}
              chat={c}
              me={me}
              defaultDir={`${state.chats_dir}/${dirName(c.jid)}`}
              onChange={(c) => setChat(i, c)}
              onRemove={() => setConfig({ chats: config.chats.filter((_, j) => j !== i) })}
            />
          ))}
          <AddChat me={me} existing={config.chats} onAdd={(c) => setConfig({ chats: [...config.chats, c] })} />
        </>
      )}
    </main>
  );
}

function LogoutModal({ who, onClose }: { who: string; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const logout = async () => {
    setBusy(true);
    setError('');
    try {
      await api.logout(); // the page switches to the QR code once the bridge has let go
    } catch (e) {
      setError(message(e));
      setBusy(false);
    }
  };
  return (
    <Modal title="Log out of WhatsApp?" onClose={onClose}>
      <p>
        This unlinks the bridge from <b>{who}</b> and stops its agents.
      </p>
      <p className="muted">
        Its chats and conversations stay saved and come back if you link this number again. Next you can scan a QR code to link
        any number.
      </p>
      {error && <p className="error small">{error}</p>}
      <div className="row end">
        <button type="button" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button type="button" className="primary danger-fill" onClick={logout} disabled={busy}>
          {busy ? 'Logging out…' : 'Log out'}
        </button>
      </div>
    </Modal>
  );
}

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [onClose]);
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title}>
        <h2>{title}</h2>
        {children}
      </div>
    </div>
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

function AgentFields<T extends Agent>({ value, onChange, defaultDir }: { value: T; onChange: (v: T) => void; defaultDir: string }) {
  const set = (patch: Partial<Agent>) => onChange(clean({ ...value, ...patch }));
  return (
    <div className="grid">
      <Field label="Engine">
        <select value={value.engine || 'claude'} onChange={(e) => set({ engine: e.target.value })}>
          {ENGINES.map((e) => (
            <option key={e} value={e}>
              {e}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Autonomy">
        <select value={value.autonomy || 'auto'} onChange={(e) => set({ autonomy: e.target.value })}>
          {Object.entries(AUTONOMY).map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Model">
        <input value={value.model ?? ''} placeholder="CLI default" onChange={(e) => set({ model: e.target.value })} />
      </Field>
      <Field label="Effort">
        <input value={value.effort ?? ''} placeholder="model default" onChange={(e) => set({ effort: e.target.value })} />
      </Field>
      <Field label="Working directory" wide>
        <DirInput value={value.workdir ?? ''} placeholder={defaultDir} onChange={(workdir) => set({ workdir })} />
      </Field>
      <Field label="System prompt" wide>
        <textarea
          rows={3}
          value={value.system_prompt ?? ''}
          placeholder="the built-in one"
          onChange={(e) => set({ system_prompt: e.target.value })}
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
          <button type="button" className="ghost" title="Use the chat’s own folder" onClick={() => onChange('')}>
            ×
          </button>
        )}
      </div>
      {problem && <span className="error small">{problem}</span>}
    </div>
  );
}

function ChatCard({ chat, me, defaultDir, onChange, onRemove }: {
  chat: Chat;
  me: string;
  defaultDir: string;
  onChange: (c: Chat) => void;
  onRemove: () => void;
}) {
  const kind = chat.jid.endsWith('@g.us') ? 'group' : chat.jid === me ? 'you' : 'person';
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
      <AgentFields value={chat} defaultDir={defaultDir} onChange={onChange} />
    </section>
  );
}

function AddChat({ me, existing, onAdd }: { me: string; existing: Chat[]; onAdd: (c: Chat) => void }) {
  const [q, setQ] = useState('');
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const have = useMemo(() => new Set(existing.map((c) => c.jid)), [existing]);
  const needle = q.trim().toLowerCase();
  const digits = q.replace(/[\s+\-().]/g, '');
  const number = /^\d{7,15}$/.test(digits) ? digits : '';
  const matches = useMemo(
    () => (groups ?? []).filter((g) => !have.has(g.jid) && g.name.toLowerCase().includes(needle)).slice(0, 8),
    [groups, have, needle],
  );
  const load = async () => {
    setBusy(true);
    setErr('');
    try {
      setGroups((await api.groups()).groups);
    } catch (e) {
      setErr(message(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    load();
  }, []);
  const add = (c: Chat) => {
    onAdd(c);
    setQ('');
  };
  const showMe = me && !have.has(me) && (!needle || 'you message yourself me'.includes(needle));
  return (
    <section className="card add">
      <h3>Add a chat</h3>
      <input value={q} placeholder="Search your groups, or type a phone number with country code" onChange={(e) => setQ(e.target.value)} />
      <ul className="options">
        {showMe && (
          <li>
            <button type="button" onClick={() => add({ jid: me, name: 'Me' })}>
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
        {matches.map((g) => (
          <li key={g.jid}>
            <button type="button" onClick={() => add({ jid: g.jid, name: g.name })}>
              {g.name} <span className="muted">{g.size} members</span>
            </button>
          </li>
        ))}
      </ul>
      <p className="muted small">
        {groups ? `${groups.length} groups` : 'Loading groups…'} ·{' '}
        <button type="button" className="link" disabled={busy} onClick={load}>
          {busy ? 'Refreshing…' : 'Refresh'}
        </button>
      </p>
      {err && <p className="error small">{err}</p>}
    </section>
  );
}
