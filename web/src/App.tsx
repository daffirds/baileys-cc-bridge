import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as KeyEvent, type ReactNode, type Ref, type SyntheticEvent } from 'react';
import { api, type Agent, type Chat, type Group, type Model, type Person, type Placeholder, type State, type Status } from './api';
import { Icon, type IconName } from './icons';

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

// A variable a system prompt may use, with what it becomes in this chat.
type Variable = Placeholder & { value: string };

function variables(state: State, chat: Chat, a: Account): Variable[] {
  const kind = isGroup(chat.jid) ? 'group' : chat.jid === a.me ? `${a.owner}'s own "Message yourself" chat` : 'direct chat';
  const values: Record<string, string> = {
    '{owner}': a.owner,
    '{number}': a.number,
    '{chat}': chat.name || chat.jid.split('@')[0],
    '{chat_kind}': kind,
    '{chat_jid}': chat.jid,
  };
  return (state.placeholders ?? []).map((p) => ({ ...p, value: values[p.key] ?? '' }));
}

// The words a variable's chip shows: {chat_kind} reads "chat kind".
const chipName = (key: string) => key.slice(1, -1).replace(/_/g, ' ');

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

// Panel lists the linked number's conversations. A card opens with its pencil and saves with its
// check; adding and removing a conversation save at once.
function Panel({ status }: { status: Status }) {
  const [state, setState] = useState<State | null>(null);
  const [chats, setChats] = useState<Chat[] | null>(null);
  const [error, setError] = useState('');
  const [leaving, setLeaving] = useState(false);
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<Chat | null>(null);
  const [toast, setToast] = useState<{ id: number; text: string } | null>(null);
  const saved = useRef<Chat[]>([]);
  const queue = useRef<Promise<unknown>>(Promise.resolve());
  const unsaved = useRef(new Set<string>()); // open cards with changes

  useEffect(() => {
    api.state().then(
      (s) => {
        saved.current = s.config.chats ?? [];
        setState(s);
        setChats(saved.current);
      },
      (e) => setError(message(e)),
    );
  }, []);

  // Leaving with a change not saved yet asks first.
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (unsaved.current.size > 0) e.preventDefault();
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, []);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2400);
    return () => clearTimeout(t);
  }, [toast]);

  // save applies a change to the saved conversations and writes them. Saves run one after another,
  // each on top of the last, so cards saved at the same time all stick.
  const save = (change: (cs: Chat[]) => Chat[], done: string) => {
    const run = queue.current.then(async () => {
      const next = change(saved.current);
      await api.save({ chats: next });
      saved.current = next;
      setChats(next);
      setToast({ id: Date.now(), text: done });
    });
    queue.current = run.catch(() => {});
    return run;
  };

  const me = status.me?.id ?? '';
  const who = status.me?.name ? `${status.me.name} · +${status.number}` : `+${status.number}`;
  const account = { me, owner: status.me?.name || 'the owner', number: status.number ?? '' };

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
          <button type="button" className="ghost" onClick={() => setLeaving(true)}>
            <Icon name="logout" size={16} />
            Log out
          </button>
          <button type="button" className="primary" disabled={!chats} onClick={() => setAdding(true)}>
            <Icon name="plus" size={16} />
            New conversation
          </button>
        </div>
      </header>
      {error && <p className="error">{error}</p>}
      {leaving && <LogoutModal who={who} onClose={() => setLeaving(false)} />}
      {removing && (
        <RemoveModal
          chat={removing}
          onRemove={() => save((cs) => cs.filter((c) => c.jid !== removing.jid), 'Removed')}
          onClose={() => setRemoving(null)}
        />
      )}
      {adding && state && chats && (
        <AddChatModal
          account={account}
          state={state}
          existing={chats}
          onAdd={(c) => save((cs) => [...cs, c], 'Added')}
          onClose={() => setAdding(false)}
        />
      )}

      {state && chats && (
        <>
          <h2 className="section">Conversations</h2>
          <p className="muted">Each conversation has its own agent. Messages anywhere else are ignored.</p>
          {chats.length === 0 && (
            <section className="card empty">
              <span className="choice-icon">
                <Icon name="chat" size={22} />
              </span>
              <p className="muted">
                No conversations yet. Add one with <b>New conversation</b>.
              </p>
            </section>
          )}
          {chats.map((c, i) => (
            <ChatCard
              key={c.jid}
              index={i}
              chat={c}
              account={account}
              state={state}
              defaultDir={`${state.chats_dir}/${dirName(c.jid)}`}
              onSave={(next) => save((cs) => cs.map((x) => (x.jid === c.jid ? next : x)), 'Saved')}
              onRemove={() => setRemoving(c)}
              onUnsaved={(u) => {
                if (u) unsaved.current.add(c.jid);
                else unsaved.current.delete(c.jid);
              }}
            />
          ))}
        </>
      )}
      {toast && (
        <div key={toast.id} className="toast" role="status">
          <Icon name="check" size={14} />
          {toast.text}
        </div>
      )}
    </main>
  );
}

function RemoveModal({ chat, onRemove, onClose }: { chat: Chat; onRemove: () => Promise<void>; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const remove = async () => {
    setBusy(true);
    setError('');
    try {
      await onRemove();
      onClose();
    } catch (e) {
      setError(message(e));
      setBusy(false);
    }
  };
  return (
    <Modal title={`Remove ${chat.name || chat.jid}?`} onClose={onClose}>
      <p>Its agent stops and its messages are ignored from now on.</p>
      <p className="muted">Its history is kept: add it again and the agent picks up where it left off.</p>
      {error && <p className="error small">{error}</p>}
      <div className="row end">
        <button type="button" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button type="button" className="primary danger-fill" onClick={remove} disabled={busy}>
          {busy ? 'Removing…' : 'Remove'}
        </button>
      </div>
    </Modal>
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

function Field({ label, wide, group, children }: { label: string; wide?: boolean; group?: boolean; children: ReactNode }) {
  const className = wide ? 'field wide' : 'field';
  // A <label> would pass a click on its caption to the group's first button.
  if (group) {
    return (
      <div className={className} role="group" aria-label={label}>
        <span>{label}</span>
        {children}
      </div>
    );
  }
  return (
    <label className={className}>
      <span>{label}</span>
      {children}
    </label>
  );
}

// Segmented picks one of a few options, with a thumb that slides to the chosen one.
function Segmented({ value, options, onChange }: { value: string; options: Record<string, string>; onChange: (v: string) => void }) {
  const keys = Object.keys(options);
  return (
    <div className="segmented" role="radiogroup" style={{ '--n': keys.length, '--i': Math.max(0, keys.indexOf(value)) } as CSSProperties}>
      <span className="thumb" />
      {keys.map((k) => (
        <button key={k} type="button" role="radio" aria-checked={k === value} className={k === value ? 'on' : ''} onClick={() => onChange(k)}>
          {options[k]}
        </button>
      ))}
    </div>
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
      <Field label="Engine" group>
        {/* Models and efforts differ per engine, so switching starts from the new one's defaults. */}
        <Segmented value={engine} options={ENGINES} onChange={(v) => v !== engine && set({ engine: v, model: '', effort: '' })} />
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
            role="switch"
            className="switch"
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
// edited. Nothing else is added to it. Its variables can be dragged in from a list beside it, which
// shows each one's value in this chat as a chip with its name under it.
function PromptField({ chat, account, state, onChange }: { chat: Chat; account: Account; state: State; onChange: (c: Chat) => void }) {
  const [listing, setListing] = useState(false);
  const editor = useRef<HTMLTextAreaElement>(null);
  const custom = !!chat.system_prompt?.trim();
  const text = custom ? chat.system_prompt! : state.default_prompt;
  // Back to the exact built-in text means following the built-in one again.
  const set = (t: string) => onChange(clean({ ...chat, system_prompt: t.trim() === state.default_prompt.trim() ? '' : t }));
  const vars = variables(state, chat, account);
  // A click on a variable puts it at the cursor, as typing would, so undo takes it back out.
  const insert = (key: string) => {
    const t = editor.current;
    if (!t) return;
    t.focus();
    if (!document.execCommand('insertText', false, key)) {
      set(t.value.slice(0, t.selectionStart) + key + t.value.slice(t.selectionEnd));
    }
  };
  return (
    <div className="prompt">
      <div className="row between-tight">
        <span className="label">System prompt</span>
        <span className="row">
          {custom && (
            <button type="button" className="link" onClick={() => set(state.default_prompt)}>
              Reset to default
            </button>
          )}
          <button type="button" className={listing ? 'toggle-chip on' : 'toggle-chip'} aria-pressed={listing} onClick={() => setListing(!listing)}>
            <Icon name="braces" size={15} />
            Variables
          </button>
        </span>
      </div>
      <div className={listing ? 'prompt-body listing' : 'prompt-body'}>
        <PromptEditor ref={editor} value={text} keys={vars.map((v) => v.key)} onChange={set} />
        {listing && (
          <aside className="vars">
            {vars.map((v) => (
              <div
                key={v.key}
                className="var"
                role="button"
                tabIndex={0}
                draggable
                title={`${v.value}\n${v.about}`}
                onDragStart={(e) => {
                  e.dataTransfer.setData('text/plain', v.key);
                  e.dataTransfer.effectAllowed = 'copy';
                }}
                onClick={() => insert(v.key)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    insert(v.key);
                  }
                }}
              >
                <span className="chip">{v.value || chipName(v.key)}</span>
                <span className="var-name">{chipName(v.key)}</span>
              </div>
            ))}
          </aside>
        )}
      </div>
    </div>
  );
}

// PromptEditor edits a prompt with its variables drawn as chips. It is a plain textarea over a
// copy of its text that draws the chips; the textarea's own text is transparent, so typing, undo,
// selection and drops stay the browser's. A chip goes as one: the caret hops over it, a selection
// takes it whole, and Backspace or Delete beside it removes it.
function PromptEditor({ ref, value, keys, onChange }: {
  ref: Ref<HTMLTextAreaElement>;
  value: string;
  keys: string[];
  onChange: (v: string) => void;
}) {
  const [dropping, setDropping] = useState(false);
  const caret = useRef(0); // where the caret last was, to tell which way it moves
  const mouse = useRef(false); // the selection is the mouse's
  const pattern = useMemo(
    () => (keys.length ? new RegExp(keys.map((k) => k.replace(/[{}]/g, '\\$&')).join('|'), 'g') : null),
    [keys.join(' ')],
  );
  const chips = useMemo(() => (pattern ? [...value.matchAll(pattern)].map((m) => [m.index, m.index + m[0].length]) : []), [value, pattern]);
  const inside = (p: number) => chips.find(([a, b]) => a < p && p < b);

  const select = (e: SyntheticEvent<HTMLTextAreaElement>) => {
    const t = e.currentTarget;
    const { selectionStart: s, selectionEnd: end } = t;
    const backward = t.selectionDirection === 'backward';
    const focus = backward ? s : end;
    let f = focus;
    const fc = inside(focus);
    if (fc) {
      // A click lands on the nearer side; arrows carry on the way they were going.
      if (mouse.current) f = focus - fc[0] < fc[1] - focus ? fc[0] : fc[1];
      else f = focus < caret.current ? fc[0] : fc[1];
    }
    let a = backward ? end : s;
    const ac = s === end ? undefined : inside(a);
    if (ac) a = backward ? ac[1] : ac[0];
    if (s === end) a = f;
    caret.current = f;
    if (f !== focus || a !== (backward ? end : s)) t.setSelectionRange(Math.min(a, f), Math.max(a, f), f < a ? 'backward' : 'forward');
  };

  const keyDown = (e: KeyEvent<HTMLTextAreaElement>) => {
    mouse.current = false;
    const t = e.currentTarget;
    if (e.metaKey || t.selectionStart !== t.selectionEnd) return;
    const p = t.selectionStart;
    const chip = e.key === 'Backspace' ? chips.find(([, b]) => b === p) : e.key === 'Delete' ? chips.find(([a]) => a === p) : undefined;
    if (chip) t.setSelectionRange(chip[0], chip[1]); // the key's own deletion then takes the chip whole
  };

  const drawn: ReactNode[] = [];
  let at = 0;
  for (const [a, b] of chips) {
    drawn.push(value.slice(at, a));
    // The braces and underscores stay in, hidden, so the copy wraps exactly like the textarea.
    drawn.push(
      <span key={a} className="chip">
        <span className="hide">{'{'}</span>
        {value
          .slice(a + 1, b - 1)
          .split('_')
          .map((w, i) => (i ? [<span key={i} className="hide">_</span>, w] : w))}
        <span className="hide">{'}'}</span>
      </span>,
    );
    at = b;
  }
  drawn.push(value.slice(at) + '\u200b'); // keeps a last empty line as tall as the textarea's

  return (
    <div className={dropping ? 'editor dropping' : 'editor'}>
      <div className="mirror" aria-hidden="true">
        {drawn}
      </div>
      <textarea
        ref={ref}
        rows={1}
        spellCheck={false}
        aria-label="System prompt"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onSelect={select}
        onKeyDown={keyDown}
        onPointerDown={() => (mouse.current = true)}
        onDragEnter={() => setDropping(true)}
        onDragLeave={() => setDropping(false)}
        onDrop={() => setDropping(false)}
      />
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

// ChatCard shows a conversation folded to its name and main settings. The pencil opens it for
// editing; the check saves the changes and folds it again, the cross drops them.
function ChatCard({ index, chat, account, state, defaultDir, onSave, onRemove, onUnsaved }: {
  index: number;
  chat: Chat;
  account: Account;
  state: State;
  defaultDir: string;
  onSave: (c: Chat) => Promise<void>;
  onRemove: () => void;
  onUnsaved: (unsaved: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(chat);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const changed = open && JSON.stringify(draft) !== JSON.stringify(chat);
  const report = useRef(onUnsaved);
  report.current = onUnsaved;
  useEffect(() => {
    report.current(changed);
  }, [changed]);
  useEffect(() => () => report.current(false), []);

  const edit = () => {
    setDraft(chat);
    setError('');
    setOpen(true);
  };
  const done = async () => {
    if (!changed) return setOpen(false);
    setBusy(true);
    setError('');
    try {
      await onSave(draft);
      setOpen(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className={open ? 'card chat open' : 'card chat'} style={{ '--i': index } as CSSProperties}>
      <div className="head">
        <Who chat={open ? draft : chat} me={account.me} />
        {!open && <Summary chat={chat} />}
        <div className="actions">
          <IconButton icon="trash" label="Remove" tone="danger" onClick={onRemove} />
          {open ? (
            <>
              <IconButton icon="x" label="Discard changes" onClick={() => setOpen(false)} disabled={busy} />
              <IconButton icon="check" label="Save" tone="primary" onClick={done} disabled={busy} />
            </>
          ) : (
            <IconButton icon="pencil" label="Edit" onClick={edit} />
          )}
        </div>
      </div>
      <div className="fold" inert={!open}>
        <div>
          <AgentFields value={draft} defaultDir={defaultDir} onChange={setDraft} />
          <PromptField chat={draft} account={account} state={state} onChange={setDraft} />
          {error && <p className="error small">Couldn’t save: {error}</p>}
        </div>
      </div>
    </section>
  );
}

// Summary sums up a folded card's agent: engine, model, and effort and permissions when set.
function Summary({ chat }: { chat: Chat }) {
  const engine = chat.engine || 'claude';
  const { models } = useModels(engine);
  const parts = [ENGINES[engine] ?? engine, chat.model ? (models?.find((m) => m.id === chat.model)?.name ?? chat.model) : 'Default model'];
  if (chat.effort) parts.push(`${effortName(chat.effort)} effort`);
  if (chat.autonomy === 'full') parts.push('Full access');
  if (chat.autonomy === 'readonly') parts.push('Read-only');
  return (
    <div className="summary">
      {parts.map((p) => (
        <span key={p} className="pill">
          {p}
        </span>
      ))}
    </div>
  );
}

function IconButton({ icon, label, tone, onClick, disabled }: {
  icon: IconName;
  label: string;
  tone?: 'primary' | 'danger';
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button type="button" className={`icon-button ${tone ?? ''}`} title={label} aria-label={label} onClick={onClick} disabled={disabled}>
      <Icon name={icon} size={18} />
    </button>
  );
}

// AddChatModal adds a chat in three steps: group or direct message, which one, then its agent and
// system prompt.
function AddChatModal({ account, state, existing, onAdd, onClose }: {
  account: Account;
  state: State;
  existing: Chat[];
  onAdd: (c: Chat) => Promise<void>;
  onClose: () => void;
}) {
  const me = account.me;
  const [step, setStep] = useState<'kind' | 'group' | 'person' | 'agent'>('kind');
  const [from, setFrom] = useState<'group' | 'person'>('group');
  const [chat, setChat] = useState<Chat | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const add = async (c: Chat) => {
    setBusy(true);
    setError('');
    try {
      await onAdd(c);
      onClose();
    } catch (e) {
      setError(message(e));
      setBusy(false);
    }
  };
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
      <div key={step} className="step">
        {step === 'kind' && (
          <div className="choices">
            <button type="button" className="choice" onClick={() => go('group')}>
              <span className="choice-icon">
                <Icon name="group" size={22} />
              </span>
              <span className="choice-text">
                <b>Group</b>
                <span className="muted">a group this number is in</span>
              </span>
              <Icon name="chevron" size={18} />
            </button>
            <button type="button" className="choice" onClick={() => go('person')}>
              <span className="choice-icon">
                <Icon name="user" size={22} />
              </span>
              <span className="choice-text">
                <b>Direct message</b>
                <span className="muted">a person, or “Message yourself”</span>
              </span>
              <Icon name="chevron" size={18} />
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
            <AgentFields value={chat} defaultDir={`${state.chats_dir}/${dirName(chat.jid)}`} onChange={setChat} />
            <PromptField chat={chat} account={account} state={state} onChange={setChat} />
            {error && <p className="error small">{error}</p>}
            <div className="row end">
              <button type="button" onClick={() => setStep(from)} disabled={busy}>
                Back
              </button>
              <button type="button" className="primary" onClick={() => add(chat)} disabled={busy}>
                {busy ? 'Adding…' : 'Add conversation'}
              </button>
            </div>
          </>
        )}
      </div>
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
