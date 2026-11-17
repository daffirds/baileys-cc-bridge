import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, type Agent, type Chat, type Config, type Group, type Model, type Person, type State, type Status } from './api';

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

// Each engine's models, asked once per page load and shared by every card.
const modelLists = new Map<string, Promise<Model[]>>();

function useModels(engine: string): { models: Model[] | null; error: string } {
  const [got, setGot] = useState<{ engine: string; models: Model[] | null; error: string }>({ engine, models: null, error: '' });
  useEffect(() => {
    let list = modelLists.get(engine);
    if (!list) {
      list = api.models(engine).then((r) => r.models);
      list.catch(() => modelLists.delete(engine)); // ask again next time
      modelLists.set(engine, list);
    }
    let alive = true;
    list.then(
      (models) => alive && setGot({ engine, models, error: '' }),
      (e) => alive && setGot({ engine, models: null, error: message(e) }),
    );
    return () => {
      alive = false;
    };
  }, [engine]);
  return got.engine === engine ? got : { models: null, error: '' };
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
  const [adding, setAdding] = useState(false);

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
          <button type="button" disabled={!config} onClick={() => setAdding(true)}>
            + Add chat
          </button>
          <button className="primary" disabled={!dirty || busy} onClick={save}>
            {busy ? 'Saving…' : dirty ? 'Save' : 'Saved'}
          </button>
        </div>
      </header>
      {error && <p className="error">{error}</p>}
      {notice && !dirty && <p className="notice">{notice}</p>}
      {leaving && <LogoutModal who={who} onClose={() => setLeaving(false)} />}
      {adding && state && config && (
        <AddChatModal
          me={me}
          existing={config.chats}
          chatsDir={state.chats_dir}
          onAdd={(c) => setConfig({ chats: [...config.chats, c] })}
          onClose={() => setAdding(false)}
        />
      )}

      {state && config && (
        <>
          <h2 className="section">Chats</h2>
          <p className="muted">Every message in these chats goes to that chat’s own agent session. Everything else is ignored.</p>
          {config.chats.length === 0 && (
            <section className="card center muted">
              No chats yet. Add one with <b>+ Add chat</b>.
            </section>
          )}
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

function Modal({ title, wide, onClose, children }: { title: string; wide?: boolean; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [onClose]);
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={wide ? 'modal wide' : 'modal'} role="dialog" aria-modal="true" aria-label={title}>
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

function AgentFields<T extends Agent>({ value, onChange, defaultDir, prompt = true }: {
  value: T;
  onChange: (v: T) => void;
  defaultDir: string;
  prompt?: boolean; // show the system prompt
}) {
  const set = (patch: Partial<Agent>) => onChange(clean({ ...value, ...patch }));
  const engine = value.engine || 'claude';
  return (
    <div className="grid">
      <Field label="Engine">
        {/* Models and efforts differ per engine, so switching starts from the new one's defaults. */}
        <select value={engine} onChange={(e) => set({ engine: e.target.value, model: '', effort: '' })}>
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
      <ModelFields engine={engine} model={value.model ?? ''} effort={value.effort ?? ''} onChange={set} />
      <Field label="Working directory" wide>
        <DirInput value={value.workdir ?? ''} placeholder={defaultDir} onChange={(workdir) => set({ workdir })} />
      </Field>
      {prompt && (
        <Field label="System prompt" wide>
          <textarea
            rows={3}
            value={value.system_prompt ?? ''}
            placeholder="the built-in one"
            onChange={(e) => set({ system_prompt: e.target.value })}
          />
        </Field>
      )}
    </div>
  );
}

// ModelFields picks a model and an effort from what the engine's CLI offers.
function ModelFields({ engine, model, effort, onChange }: {
  engine: string;
  model: string;
  effort: string;
  onChange: (patch: Partial<Agent>) => void;
}) {
  const { models, error } = useModels(engine);
  const chosen = models?.find((m) => m.id === model) ?? models?.[0];
  const efforts = chosen?.efforts ?? [];
  const setModel = (id: string) => {
    const next = models?.find((m) => m.id === id);
    // Keep the effort only if the new model takes it.
    onChange({ model: id, effort: next?.efforts?.includes(effort) ? effort : '' });
  };
  if (!models) {
    return (
      <>
        <Field label="Model">
          <select disabled value="">
            <option value="">{error ? 'could not list models' : 'Loading…'}</option>
          </select>
        </Field>
        <Field label="Effort">
          <select disabled value="">
            <option value="">{error ? '' : 'Loading…'}</option>
          </select>
        </Field>
        {error && <span className="error small wide-cell">{error}</span>}
      </>
    );
  }
  return (
    <>
      <Field label="Model">
        <select value={model} title={chosen?.description} onChange={(e) => setModel(e.target.value)}>
          {models.map((m) => (
            <option key={m.id} value={m.id} title={m.description}>
              {m.name}
            </option>
          ))}
          {model && !models.some((m) => m.id === model) && <option value={model}>{model} (not offered)</option>}
        </select>
      </Field>
      <Field label="Effort">
        <select value={effort} disabled={efforts.length === 0 && !effort} onChange={(e) => onChange({ effort: e.target.value })}>
          <option value="">{chosen?.default_effort ? `Model default (${chosen.default_effort})` : 'Model default'}</option>
          {efforts.map((e) => (
            <option key={e} value={e}>
              {e}
            </option>
          ))}
          {effort && !efforts.includes(effort) && <option value={effort}>{effort} (not offered)</option>}
        </select>
      </Field>
    </>
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

// AddChatModal adds a chat in three steps: group or direct message, which one, then its agent.
function AddChatModal({ me, existing, chatsDir, onAdd, onClose }: {
  me: string;
  existing: Chat[];
  chatsDir: string;
  onAdd: (c: Chat) => void;
  onClose: () => void;
}) {
  const [step, setStep] = useState<'kind' | 'group' | 'person' | 'agent'>('kind');
  const [from, setFrom] = useState<'group' | 'person'>('group');
  const [chat, setChat] = useState<Chat | null>(null);
  const have = useMemo(() => new Set(existing.map((c) => c.jid)), [existing]);
  const pick = (c: Chat) => {
    setChat(c);
    setStep('agent');
  };
  const go = (kind: 'group' | 'person') => {
    setFrom(kind);
    setStep(kind);
  };
  return (
    <Modal title={chat && step === 'agent' ? `Add ${chat.name}` : 'Add a chat'} wide={step === 'agent'} onClose={onClose}>
      {step === 'kind' && (
        <div className="choices">
          <button type="button" className="choice" onClick={() => go('group')}>
            <b>Group</b>
            <span className="muted">a group this number is in</span>
          </button>
          <button type="button" className="choice" onClick={() => go('person')}>
            <b>Direct message</b>
            <span className="muted">a person, or your own “Message yourself” chat</span>
          </button>
        </div>
      )}
      {step === 'group' && <GroupPicker have={have} onPick={pick} onBack={() => setStep('kind')} />}
      {step === 'person' && <PersonPicker me={me} have={have} onPick={pick} onBack={() => setStep('kind')} />}
      {step === 'agent' && chat && (
        <>
          <div className="grid">
            <Field label="Name" wide>
              <input value={chat.name ?? ''} onChange={(e) => setChat({ ...chat, name: e.target.value })} />
            </Field>
          </div>
          <AgentFields value={chat} prompt={false} defaultDir={`${chatsDir}/${dirName(chat.jid)}`} onChange={setChat} />
          <p className="muted small">The system prompt can be changed on the chat’s card afterwards.</p>
          <div className="row end">
            <button type="button" onClick={() => setStep(from)}>
              Back
            </button>
            <button
              type="button"
              className="primary"
              onClick={() => {
                onAdd(chat);
                onClose();
              }}
            >
              Add chat
            </button>
          </div>
        </>
      )}
    </Modal>
  );
}

function GroupPicker({ have, onPick, onBack }: { have: Set<string>; onPick: (c: Chat) => void; onBack: () => void }) {
  const [q, setQ] = useState('');
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [err, setErr] = useState('');
  const load = () => {
    setErr('');
    api.groups().then(
      (r) => setGroups(r.groups),
      (e) => setErr(message(e)),
    );
  };
  useEffect(load, []);
  const needle = q.trim().toLowerCase();
  const list = (groups ?? []).filter((g) => !have.has(g.jid) && g.name.toLowerCase().includes(needle));
  return (
    <>
      <input autoFocus value={q} placeholder="Search your groups" onChange={(e) => setQ(e.target.value)} />
      <ul className="options scroll">
        {list.slice(0, 100).map((g) => (
          <li key={g.jid}>
            <button type="button" onClick={() => onPick({ jid: g.jid, name: g.name })}>
              {g.name} <span className="muted">{g.size === 1 ? '1 member' : `${g.size} members`}</span>
            </button>
          </li>
        ))}
      </ul>
      {!groups && !err && <p className="muted small">Loading your groups…</p>}
      {groups && list.length === 0 && <p className="muted small">No group matches.</p>}
      {err && <p className="error small">{err}</p>}
      <div className="row between">
        <button type="button" onClick={onBack}>
          Back
        </button>
        <button type="button" className="link" onClick={load}>
          Refresh
        </button>
      </div>
    </>
  );
}

function PersonPicker({ me, have, onPick, onBack }: { me: string; have: Set<string>; onPick: (c: Chat) => void; onBack: () => void }) {
  const [q, setQ] = useState('');
  const [people, setPeople] = useState<Person[] | null>(null);
  useEffect(() => {
    api.people().then(
      (r) => setPeople(r.people),
      () => setPeople([]),
    );
  }, []);
  const needle = q.trim().toLowerCase();
  const digits = q.replace(/[\s+\-().]/g, '');
  const number = /^\d{7,15}$/.test(digits) ? digits : '';
  const list = (people ?? []).filter(
    (p) => !have.has(p.jid) && ((p.name ?? '').toLowerCase().includes(needle) || (/^\d+$/.test(digits) && p.jid.startsWith(digits))),
  );
  const typed = number && !have.has(`${number}@s.whatsapp.net`) && !list.some((p) => p.jid === `${number}@s.whatsapp.net`);
  const showMe = me && !have.has(me) && (!needle || 'you me message yourself'.includes(needle));
  return (
    <>
      <input autoFocus value={q} placeholder="Search by name, or type a phone number with country code" onChange={(e) => setQ(e.target.value)} />
      <ul className="options scroll">
        {showMe && (
          <li>
            <button type="button" onClick={() => onPick({ jid: me, name: 'Me' })}>
              You <span className="muted">your “Message yourself” chat</span>
            </button>
          </li>
        )}
        {typed && (
          <li>
            <button type="button" onClick={() => onPick({ jid: `${number}@s.whatsapp.net`, name: `+${number}` })}>
              +{number} <span className="muted">this number</span>
            </button>
          </li>
        )}
        {list.slice(0, 100).map((p) => (
          <li key={p.jid}>
            <button type="button" onClick={() => onPick({ jid: p.jid, name: p.name || `+${p.jid.split('@')[0]}` })}>
              {p.name} <span className="muted">+{p.jid.split('@')[0]}</span>
            </button>
          </li>
        ))}
      </ul>
      {!people && <p className="muted small">Loading…</p>}
      {people && people.length === 0 && !number && (
        <p className="muted small">No names known yet: type the number. Names show up here once people write in a chat.</p>
      )}
      <div className="row between">
        <button type="button" onClick={onBack}>
          Back
        </button>
      </div>
    </>
  );
}
