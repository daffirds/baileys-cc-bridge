import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { api, type Agent, type Chat, type Config, type Group, type Model, type Person, type State, type Status } from './api';
import { Icon } from './icons';

const ENGINES: Record<string, string> = { claude: 'Claude', codex: 'Codex' };
const AUTONOMY: Record<string, string> = {
  auto: 'Auto — a safety check reviews risky steps',
  full: 'Full access — no prompts, no sandbox',
  readonly: 'Read-only — can look, can’t change',
};

const effortName = (e: string) => (e === 'xhigh' ? 'Extra high' : e.charAt(0).toUpperCase() + e.slice(1));

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

// Who the prompt speaks of, to fill in its placeholders the way the bridge does.
type Account = { me: string; owner: string; number: string };

function fillPrompt(prompt: string, chat: Chat, a: Account) {
  const kind = chat.jid.endsWith('@g.us') ? 'group' : chat.jid === a.me ? `${a.owner}'s own "Message yourself" chat` : 'direct chat';
  const values: Record<string, string> = {
    '{owner}': a.owner,
    '{number}': a.number,
    '{chat}': chat.name || chat.jid.split('@')[0],
    '{chat_kind}': kind,
    '{chat_jid}': chat.jid,
  };
  return prompt.replace(/\{(owner|number|chat_kind|chat_jid|chat)\}/g, (m) => values[m] ?? m);
}

const isGroup = (jid: string) => jid.endsWith('@g.us');

// Avatar stands in for a chat's picture: a group icon, a person's initials, or you.
function Avatar({ chat, me, small }: { chat: Chat; me: string; small?: boolean }) {
  const you = chat.jid === me;
  const initials = (chat.name ?? '')
    .split(/\s+/)
    .filter((w) => /^\p{L}/u.test(w))
    .slice(0, 2)
    .map((w) => w[0].toUpperCase())
    .join('');
  let hue = 0;
  for (const c of chat.jid) hue = (hue * 31 + c.charCodeAt(0)) % 360;
  return (
    <span className={`avatar${you ? ' you' : ''}${small ? ' small' : ''}`} style={{ '--h': hue } as CSSProperties} aria-hidden="true">
      {isGroup(chat.jid) ? <Icon name="group" size={small ? 17 : 20} /> : you || !initials ? <Icon name="user" size={small ? 17 : 20} /> : initials}
    </span>
  );
}

// Who names a chat, with its JID in small print under the name.
function Who({ chat, me }: { chat: Chat; me: string }) {
  return (
    <>
      <Avatar chat={chat} me={me} />
      <div className="who">
        <span className="title">{chat.name || chat.jid.split('@')[0]}</span>
        <span className="jid">{chat.jid}</span>
      </div>
    </>
  );
}

// Option is a chat to pick in the new conversation modal. A long name is cut short with "…".
function Option({ chat, me, label, detail, onPick }: { chat: Chat; me: string; label: string; detail: string; onPick: (c: Chat) => void }) {
  return (
    <li>
      <button type="button" title={label} onClick={() => onPick(chat)}>
        <Avatar chat={chat} me={me} small />
        <span className="option-name">{label}</span>
        <span className="option-detail">{detail}</span>
      </button>
    </li>
  );
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
        <Brand />
      </header>
      <Linking status={status} />
      {error && <p className="error">{error}</p>}
    </main>
  );
}

function Brand({ children }: { children?: ReactNode }) {
  return (
    <div className="brand">
      <span className="logo">
        <Icon name="chat" size={22} />
      </span>
      <div>
        <h1>baileys-cc-bridge</h1>
        {children}
      </div>
    </div>
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

// useAutosave saves the config a moment after it stops changing. Saves run one at a time, each with
// the latest config, so the file always ends up with what the page shows.
function useAutosave(config: Config | null, initial: string | null) {
  const latest = useRef(config);
  latest.current = config;
  const saved = useRef(initial);
  const running = useRef(false);
  const [phase, setPhase] = useState<'saved' | 'pending' | 'saving' | 'failed'>('saved');
  const [error, setError] = useState('');
  if (saved.current === null && initial !== null) saved.current = initial;

  const flush = useCallback(async () => {
    if (running.current) return; // the running loop takes the latest config itself
    running.current = true;
    try {
      for (;;) {
        const c = latest.current;
        const json = JSON.stringify(c);
        if (!c || json === saved.current) break;
        setPhase('saving');
        try {
          await api.save(c);
          saved.current = json;
          setError('');
        } catch (e) {
          setError(message(e));
          setPhase('failed');
          return; // the next change tries again
        }
      }
      setPhase('saved');
    } finally {
      running.current = false;
    }
  }, []);

  const json = JSON.stringify(config);
  const dirty = config !== null && saved.current !== null && json !== saved.current;
  useEffect(() => {
    if (!dirty) return;
    setPhase((p) => (p === 'saving' ? p : 'pending'));
    const t = setTimeout(flush, 700);
    return () => clearTimeout(t);
  }, [json, dirty, flush]);

  // Leaving with a change not saved yet asks first.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  return { phase: dirty && phase === 'saved' ? 'pending' : phase, error };
}

function Panel({ status }: { status: Status }) {
  const [state, setState] = useState<State | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [initial, setInitial] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [leaving, setLeaving] = useState(false);
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<Chat | null>(null);

  useEffect(() => {
    api.state().then(
      (s) => {
        const c = { chats: s.config.chats ?? [] };
        setState(s);
        setConfig(c);
        setInitial(JSON.stringify(c));
      },
      (e) => setError(message(e)),
    );
  }, []);
  const save = useAutosave(config, initial);

  const me = status.me?.id ?? '';
  const who = status.me?.name ? `${status.me.name} · +${status.number}` : `+${status.number}`;
  const setChat = (i: number, c: Chat) => config && setConfig({ chats: config.chats.map((x, j) => (j === i ? c : x)) });

  return (
    <main className="page">
      <header className="top">
        <Brand>
          <p className="account">
            <span className="dot" />
            {status.me?.name ? (
              <span>
                <b>{status.me.name}</b> · +{status.number}
              </span>
            ) : (
              <b>+{status.number}</b>
            )}
          </p>
        </Brand>
        <div className="row">
          {config && (
            <span className={`save ${save.phase}`} title="The bridge applies changes between turns.">
              {{ saved: 'All changes saved', pending: 'Unsaved…', saving: 'Saving…', failed: 'Not saved' }[save.phase]}
            </span>
          )}
          <button type="button" className="ghost" onClick={() => setLeaving(true)}>
            <Icon name="logout" size={16} />
            Log out
          </button>
          <button type="button" className="primary" disabled={!config} onClick={() => setAdding(true)}>
            <Icon name="plus" size={16} />
            New conversation
          </button>
        </div>
      </header>
      {error && <p className="error">{error}</p>}
      {save.error && <p className="error">Could not save: {save.error}</p>}
      {leaving && <LogoutModal who={who} onClose={() => setLeaving(false)} />}
      {removing && config && (
        <Modal title={`Remove ${removing.name || removing.jid}?`} onClose={() => setRemoving(null)}>
          <p>Its agent stops and its messages are ignored from now on.</p>
          <p className="muted">Its history is kept: add it again and the agent picks up where it left off.</p>
          <div className="row end">
            <button type="button" onClick={() => setRemoving(null)}>
              Cancel
            </button>
            <button
              type="button"
              className="primary danger-fill"
              onClick={() => {
                setConfig({ chats: config.chats.filter((c) => c.jid !== removing.jid) });
                setRemoving(null);
              }}
            >
              Remove
            </button>
          </div>
        </Modal>
      )}
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
          <h2 className="section">Conversations</h2>
          <p className="muted">Each conversation has its own agent. Messages anywhere else are ignored.</p>
          {config.chats.length === 0 && (
            <section className="card center muted">
              No conversations yet. Add one with <b>New conversation</b>.
            </section>
          )}
          {config.chats.map((c, i) => (
            <ChatCard
              key={c.jid}
              chat={c}
              account={{ me, owner: status.me?.name || 'the owner', number: status.number ?? '' }}
              state={state}
              defaultDir={`${state.chats_dir}/${dirName(c.jid)}`}
              onChange={(c) => setChat(i, c)}
              onRemove={() => setRemoving(c)}
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

// AgentFields edits how a chat's agent runs. A direct chat can be renamed; a group keeps the
// name it has on WhatsApp.
function AgentFields({ value, onChange, defaultDir }: { value: Chat; onChange: (c: Chat) => void; defaultDir: string }) {
  const set = (patch: Partial<Chat>) => onChange(clean({ ...value, ...patch }));
  const engine = value.engine || 'claude';
  return (
    <div className="grid">
      {!isGroup(value.jid) && (
        <Field label="Name">
          <input value={value.name ?? ''} onChange={(e) => onChange({ ...value, name: e.target.value })} />
        </Field>
      )}
      <Field label="Engine">
        {/* Models and efforts differ per engine, so switching starts from the new one's defaults. */}
        <select value={engine} onChange={(e) => set({ engine: e.target.value, model: '', effort: '' })}>
          {Object.entries(ENGINES).map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Permissions">
        <select value={value.autonomy || 'auto'} onChange={(e) => set({ autonomy: e.target.value })}>
          {Object.entries(AUTONOMY).map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      </Field>
      <ModelFields engine={engine} model={value.model ?? ''} effort={value.effort ?? ''} onChange={set} />
      <Field label="Footer">
        <span className="toggle">
          <input
            type="checkbox"
            checked={value.footer !== false}
            onChange={(e) => set({ footer: e.target.checked ? undefined : false })}
          />
          <i>Assisted by {engine === 'codex' ? 'Codex' : 'Claude'}</i>
        </span>
      </Field>
      <Field label="Folder" wide>
        <DirInput value={value.workdir ?? ''} placeholder={defaultDir} onChange={(workdir) => set({ workdir })} />
      </Field>
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
            <option value="">{error ? 'Couldn’t load the models' : 'Loading…'}</option>
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
          <option value="">{chosen?.default_effort ? `Model default (${effortName(chosen.default_effort)})` : 'Model default'}</option>
          {efforts.map((e) => (
            <option key={e} value={e}>
              {effortName(e)}
            </option>
          ))}
          {effort && !efforts.includes(effort) && <option value={effort}>{effort} (not offered)</option>}
        </select>
      </Field>
    </>
  );
}

// PromptField shows the whole system prompt the chat's agent gets: the built-in one until it is
// edited. Nothing else is added to it.
function PromptField({ chat, account, state, onChange }: { chat: Chat; account: Account; state: State; onChange: (c: Chat) => void }) {
  const [preview, setPreview] = useState(false);
  const custom = !!chat.system_prompt?.trim();
  const text = custom ? chat.system_prompt! : state.default_prompt;
  // Back to the exact built-in text means following the built-in one again.
  const set = (t: string) => onChange(clean({ ...chat, system_prompt: t.trim() === state.default_prompt.trim() ? '' : t }));
  return (
    <div className="prompt">
      <div className="row between-tight">
        <span className="label">System prompt</span>
        <span className="row">
          {custom && (
            <button type="button" className="link" onClick={() => set(state.default_prompt)}>
              Reset to built-in
            </button>
          )}
          <button type="button" className="link" onClick={() => setPreview(!preview)}>
            {preview ? 'Edit' : 'Preview'}
          </button>
        </span>
      </div>
      {preview ? (
        <pre className="preview">{fillPrompt(text, chat, account)}</pre>
      ) : (
        <textarea rows={12} spellCheck={false} value={text} onChange={(e) => set(e.target.value)} />
      )}
      <p className="muted small">
        Filled in for this conversation:{' '}
        {(state.placeholders ?? []).map((p, i) => (
          <span key={p.key} title={p.about}>
            {i > 0 && ' '}
            <code>{p.key}</code>
          </span>
        ))}
        . Clearing it all goes back to the built-in one.
      </p>
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
        <input value={value} placeholder={placeholder} spellCheck={false} onChange={(e) => onChange(e.target.value)} />
        <button type="button" onClick={choose}>
          Choose…
        </button>
        {value && (
          <button type="button" className="ghost" title="Use the conversation’s own folder" onClick={() => onChange('')}>
            ×
          </button>
        )}
      </div>
      {problem && <span className="error small">{problem}</span>}
    </div>
  );
}

function ChatCard({ chat, account, state, defaultDir, onChange, onRemove }: {
  chat: Chat;
  account: Account;
  state: State;
  defaultDir: string;
  onChange: (c: Chat) => void;
  onRemove: () => void;
}) {
  return (
    <section className="card">
      <div className="row head">
        <Who chat={chat} me={account.me} />
        <button type="button" className="ghost danger" onClick={onRemove}>
          Remove
        </button>
      </div>
      <AgentFields value={chat} defaultDir={defaultDir} onChange={onChange} />
      <PromptField chat={chat} account={account} state={state} onChange={onChange} />
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
    <Modal title="New conversation" wide={step === 'agent'} onClose={onClose}>
      {step === 'kind' && (
        <div className="choices">
          <button type="button" className="choice" onClick={() => go('group')}>
            <b>Group</b>
            <span className="muted">a group this number is in</span>
          </button>
          <button type="button" className="choice" onClick={() => go('person')}>
            <b>Direct message</b>
            <span className="muted">a person, or “Message yourself”</span>
          </button>
        </div>
      )}
      {step === 'group' && <GroupPicker have={have} onPick={pick} onBack={() => setStep('kind')} />}
      {step === 'person' && <PersonPicker me={me} have={have} onPick={pick} onBack={() => setStep('kind')} />}
      {step === 'agent' && chat && (
        <>
          <div className="row head">
            <Who chat={chat} me={me} />
          </div>
          <AgentFields value={chat} defaultDir={`${chatsDir}/${dirName(chat.jid)}`} onChange={setChat} />
          <p className="muted small">The system prompt can be changed on the conversation’s card afterwards.</p>
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
              Add conversation
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
          <Option
            key={g.jid}
            chat={{ jid: g.jid, name: g.name }}
            me=""
            label={g.name}
            detail={g.size === 1 ? '1 member' : `${g.size} members`}
            onPick={onPick}
          />
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
        {showMe && <Option chat={{ jid: me, name: 'Me' }} me={me} label="You" detail="“Message yourself”" onPick={onPick} />}
        {typed && (
          <Option chat={{ jid: `${number}@s.whatsapp.net`, name: `+${number}` }} me={me} label={`+${number}`} detail="this number" onPick={onPick} />
        )}
        {list.slice(0, 100).map((p) => (
          <Option
            key={p.jid}
            chat={{ jid: p.jid, name: p.name || `+${p.jid.split('@')[0]}` }}
            me={me}
            label={p.name || `+${p.jid.split('@')[0]}`}
            detail={`+${p.jid.split('@')[0]}`}
            onPick={onPick}
          />
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
