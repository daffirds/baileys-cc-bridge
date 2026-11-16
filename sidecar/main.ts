// The WhatsApp side of bailey-cc-bridge. The Go daemon runs this file with node and talks to it in
// JSON lines: requests on stdin, responses and events on stdout. It holds the Baileys connection and
// nothing else. The whitelist, the sessions and the agents all live on the Go side.
//
// Events:    {"type":"qr","qr":"…"}  {"type":"pairing_code","code":"…"}
//            {"type":"ready","me":{"id":"…","lid":"…","name":"…"}}  {"type":"closed","reason":"…"}
//            {"type":"message","message":{…}}  (see Incoming)
// Requests:  {"id":1,"method":"send_text","params":{…}}
//            (methods: send_text, send_media, react, chat_info, download, chats, logout)
// Responses: {"type":"response","id":1,"result":{…}}  or  {"type":"response","id":1,"error":"…"}

import { createWriteStream, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { basename, dirname, join } from 'node:path';
import { createInterface } from 'node:readline';
import { pipeline } from 'node:stream/promises';
import { format, parseArgs } from 'node:util';
import makeWASocket, {
	Browsers,
	DisconnectReason,
	downloadMediaMessage,
	fetchLatestBaileysVersion,
	getContentType,
	isJidBroadcast,
	isJidGroup,
	isJidNewsletter,
	isLidUser,
	jidNormalizedUser,
	makeCacheableSignalKeyStore,
	normalizeMessageContent,
	proto,
	toNumber,
	useMultiFileAuthState,
	type AnyMessageContent,
	type Contact,
	type GroupMetadata,
	type MiscMessageGenerationOptions,
	type WAMessage,
	type WASocket,
} from 'baileys';
import pino from 'pino';
import sharp from 'sharp';

// stdout carries the protocol. Anything a library prints to the console (libsignal does) goes to stderr.
for (const level of ['log', 'info', 'warn', 'error', 'debug', 'trace'] as const) {
	console[level] = (...args: unknown[]) => {
		process.stderr.write(format(...args) + '\n');
	};
}

const { values: opts } = parseArgs({
	options: {
		auth: { type: 'string' }, // Baileys credentials directory
		phone: { type: 'string' }, // link with a pairing code for this number instead of a QR code
		'log-level': { type: 'string', default: 'warn' },
	},
});
if (!opts.auth) {
	console.error('usage: node main.ts --auth <dir> [--phone <number>] [--log-level <pino level>]');
	process.exit(2);
}

const logger = pino({ level: opts['log-level'] }, pino.destination(2));

type Json = Record<string, unknown>;

const emit = (msg: Json, then?: () => void) => {
	process.stdout.write(JSON.stringify(msg) + '\n', then);
};

const { state, saveCreds } = await useMultiFileAuthState(opts.auth);
// The WhatsApp Web version a Baileys release ships with goes stale. This asks for the current one
// and falls back to the built-in one when offline.
const { version } = await fetchLatestBaileysVersion();

let sock!: WASocket;
let stopping = false;
let pairingRequested = false;

function connect() {
	sock = makeWASocket({
		version,
		auth: { creds: state.creds, keys: makeCacheableSignalKeyStore(state.keys, logger) },
		logger,
		browser: Browsers.macOS('Desktop'),
		// Not a chat client: never show as online (so the phone keeps its notifications) and never
		// pull old history.
		markOnlineOnConnect: false,
		syncFullHistory: false,
		// Required on Baileys 7: without it, a message whose encryption session has to be
		// re-established is dropped instead of retried. A retry of our own message resends it.
		getMessage: async (key) => recent.get(key.id ?? '')?.message ?? { conversation: '' },
	});
	sock.ev.on('creds.update', saveCreds);
	sock.ev.on('contacts.upsert', learnContacts);
	sock.ev.on('contacts.update', learnContacts);
	sock.ev.on('connection.update', ({ connection, lastDisconnect, qr }) => {
		if (qr) onQR(qr);
		if (connection === 'open') {
			const me = sock.user;
			emit({
				type: 'ready',
				me: {
					id: jidNormalizedUser(me?.id),
					lid: me?.lid ? jidNormalizedUser(me.lid) : undefined,
					name: me?.name ?? me?.notify ?? me?.verifiedName,
				},
			});
		}
		if (connection !== 'close' || stopping) return;
		const code = (lastDisconnect?.error as { output?: { statusCode?: number } } | undefined)?.output?.statusCode;
		if (code === DisconnectReason.loggedOut || code === DisconnectReason.forbidden) {
			const reason = code === DisconnectReason.loggedOut ? 'logged_out' : 'forbidden';
			emit({ type: 'closed', reason }, () => process.exit(3));
			return;
		}
		// 515 is normal right after linking; anything else is a dropped connection. Reconnect either way.
		logger.warn({ code }, 'connection closed; reconnecting');
		setTimeout(connect, code === DisconnectReason.restartRequired ? 0 : 3000);
	});
	sock.ev.on('messages.upsert', ({ messages, type }) => {
		// 'append' is what WhatsApp queued while we were offline, plus our own sends: neither is new
		// to anyone, so only live messages ('notify') go on.
		if (type !== 'notify') return;
		for (const m of messages) {
			// One chain keeps delivery in order while a message waits on a LID lookup.
			tail = tail.then(() => onMessage(m)).catch((err: unknown) => logger.warn({ err }, 'could not process a message'));
		}
	});
}

function onQR(qr: string) {
	if (!opts.phone) {
		emit({ type: 'qr', qr });
		return;
	}
	if (pairingRequested) return;
	pairingRequested = true;
	sock.requestPairingCode(opts.phone.replace(/\D/g, '')).then(
		(code) => emit({ type: 'pairing_code', code }),
		(err: unknown) => emit({ type: 'closed', reason: `pairing code request failed: ${String(err)}` }, () => process.exit(3)),
	);
}

// --- incoming messages ---

let tail = Promise.resolve();

// Recent messages by id: downloads, quotes and reactions need the original.
const recent = new Map<string, WAMessage>();

function remember(m: WAMessage | undefined) {
	const id = m?.key?.id;
	if (!m || !id) return;
	recent.delete(id);
	recent.set(id, m);
	if (recent.size > 2000) recent.delete(recent.keys().next().value!);
}

type Incoming = {
	id: string;
	chat: string; // the phone-number JID of a 1:1 chat when known, …@g.us for a group
	chat_alt?: string; // the @lid JID WhatsApp used for the chat, if different
	sender: string;
	from_me: boolean; // sent from this account: typed on the phone or another linked device
	push_name?: string;
	timestamp: number; // unix seconds
} & Body;

type Body = {
	kind: string; // text | image | video | audio | document | sticker | location | contact | poll | reaction | edit | revoke | unsupported
	text?: string; // text, caption, or the new text of an edit
	quoted?: { id: string; sender?: string; sender_name?: string; text?: string };
	forwarded?: 'yes' | 'many';
	mentions?: { jid: string; name?: string }[]; // @mentioned people; their @tokens in text use these numbers
	media?: { mimetype?: string; size?: number; file_name?: string; seconds?: number; voice?: boolean };
	reaction?: { id: string; emoji: string }; // emoji '' = removed
	target?: string; // edit, revoke: the id of the message changed
};

async function onMessage(m: WAMessage) {
	const key = m.key;
	const remote = key.remoteJid;
	if (!key.id || !remote || isJidBroadcast(remote) || isJidNewsletter(remote)) return;
	const content = normalizeMessageContent(m.message);
	const body = content && describe(content);
	if (!body) return; // a stub that failed to decrypt, or protocol noise with nothing to say
	remember(m);
	const group = Boolean(isJidGroup(remote));
	const chat = group ? remote : await phoneJid(remote, key.remoteJidAlt);
	const raw = jidNormalizedUser(remote);
	const sender = key.fromMe ? jidNormalizedUser(sock.user?.id) : group ? await phoneJid(key.participant, key.participantAlt) : chat;
	if (!key.fromMe) learnName(sender, m.pushName);
	const ctx = contextOf(content);
	for (const j of ctx?.mentionedJid ?? []) {
		const pn = await phoneJid(j, undefined);
		// In groups that hide numbers, the text says @<private id>; show the number instead.
		if (body.text && pn !== jidNormalizedUser(j)) body.text = body.text.replaceAll('@' + userOf(j), '@' + userOf(pn));
		(body.mentions ??= []).push({ jid: pn, name: names.get(pn) });
	}
	if (body.quoted?.sender) {
		body.quoted.sender = await phoneJid(body.quoted.sender, undefined);
		body.quoted.sender_name = names.get(body.quoted.sender);
	}
	const out: Incoming = {
		id: key.id,
		chat,
		chat_alt: raw !== chat ? raw : undefined,
		sender,
		from_me: Boolean(key.fromMe),
		push_name: m.pushName ?? undefined,
		timestamp: toNumber(m.messageTimestamp),
		...body,
	};
	emit({ type: 'message', message: out });
}

// People's names by JID: the name saved in the address book when WhatsApp synced one, else the
// name they gave themselves. Kept in names.json next to the credentials, so a restart doesn't
// forget everyone.
const names = new Map<string, string>();
const savedNames = new Set<string>();
const namesFile = join(dirname(opts.auth), 'names.json');
let namesDirty = false;

try {
	const stored = JSON.parse(readFileSync(namesFile, 'utf8')) as { names?: Record<string, string>; saved?: string[] };
	for (const [jid, name] of Object.entries(stored.names ?? {})) names.set(jid, name);
	for (const jid of stored.saved ?? []) savedNames.add(jid);
} catch {
	// first run, or unreadable: start empty
}

function saveNames() {
	if (!namesDirty) return;
	namesDirty = false;
	try {
		writeFileSync(namesFile + '.tmp', JSON.stringify({ names: Object.fromEntries(names), saved: [...savedNames] }), { mode: 0o600 });
		renameSync(namesFile + '.tmp', namesFile);
	} catch (err) {
		logger.warn({ err }, 'could not save names');
	}
}
setInterval(saveNames, 10_000).unref();

function learnName(jid: string | null | undefined, name: string | null | undefined, saved = false) {
	if (!jid || !name) return;
	const key = jidNormalizedUser(jid);
	if (saved) savedNames.add(key);
	else if (savedNames.has(key)) return;
	if (names.get(key) === name) return;
	names.set(key, name);
	namesDirty = true;
}

function learnContacts(contacts: Partial<Contact>[]) {
	for (const c of contacts) {
		const jid = c.phoneNumber ?? c.id;
		if (c.name) learnName(jid, c.name, true);
		else learnName(jid, c.notify ?? c.verifiedName);
	}
}

const userOf = (jid: string) => jid.split('@')[0].split(':')[0];

function contextOf(c: proto.IMessage): proto.IContextInfo | undefined {
	const type = getContentType(c);
	return type ? (((c[type] ?? {}) as Node).contextInfo ?? undefined) : undefined;
}

// phoneJid turns a @lid JID into its phone-number JID when WhatsApp sent the alternative along or
// Baileys has learned the mapping; otherwise it keeps the @lid.
async function phoneJid(jid: string | null | undefined, alt: string | null | undefined): Promise<string> {
	if (!jid) return '';
	if (!isLidUser(jid)) return jidNormalizedUser(jid);
	if (sock.user?.lid && jidNormalizedUser(jid) === jidNormalizedUser(sock.user.lid)) return jidNormalizedUser(sock.user.id);
	if (alt && !isLidUser(alt)) return jidNormalizedUser(alt);
	const pn = await sock.signalRepository.lidMapping.getPNForLID(jid).catch(() => null);
	return jidNormalizedUser(pn ?? jid);
}

const MEDIA: Record<string, string> = {
	imageMessage: 'image',
	videoMessage: 'video',
	ptvMessage: 'video',
	audioMessage: 'audio',
	documentMessage: 'document',
	stickerMessage: 'sticker',
};

// Message types that carry nothing worth telling an agent. An album header only announces how many
// photos and videos follow; each of them arrives as its own message.
const IGNORED = new Set([
	'albumMessage',
	'pollUpdateMessage',
	'keepInChatMessage',
	'pinInChatMessage',
	'encReactionMessage',
	'encEventResponseMessage',
	'messageHistoryBundle',
]);

type Node = {
	caption?: string | null;
	mimetype?: string | null;
	fileLength?: number | Long | null;
	fileName?: string | null;
	seconds?: number | null;
	ptt?: boolean | null;
	contextInfo?: proto.IContextInfo | null;
};
type Long = Parameters<typeof toNumber>[0];

function describe(c: proto.IMessage): Body | undefined {
	if (c.reactionMessage?.key?.id) {
		return { kind: 'reaction', reaction: { id: c.reactionMessage.key.id, emoji: c.reactionMessage.text ?? '' } };
	}
	const p = c.protocolMessage;
	if (p) {
		if (!p.key?.id) return undefined;
		if (p.type === proto.Message.ProtocolMessage.Type.REVOKE) return { kind: 'revoke', target: p.key.id };
		if (p.type === proto.Message.ProtocolMessage.Type.MESSAGE_EDIT) {
			return { kind: 'edit', target: p.key.id, text: textOf(normalizeMessageContent(p.editedMessage)) };
		}
		return undefined;
	}
	const type = getContentType(c);
	if (!type || IGNORED.has(type)) return undefined;
	const node = (c[type] ?? {}) as Node;
	const body = contentOf(c, type, node);
	const ctx = node.contextInfo;
	if (ctx?.stanzaId) {
		body.quoted = {
			id: ctx.stanzaId,
			sender: ctx.participant ? jidNormalizedUser(ctx.participant) : undefined,
			text: excerpt(textOf(normalizeMessageContent(ctx.quotedMessage))),
		};
	}
	// WhatsApp never says who wrote a forwarded message, only that it was forwarded (and "many times"
	// from the fifth hop on).
	if (ctx?.isForwarded) body.forwarded = (ctx.forwardingScore ?? 0) >= 5 ? 'many' : 'yes';
	return body;
}

function contentOf(c: proto.IMessage, type: string, node: Node): Body {
	if (type === 'conversation') return { kind: 'text', text: c.conversation ?? '' };
	if (type === 'extendedTextMessage') return { kind: 'text', text: c.extendedTextMessage?.text ?? '' };
	if (MEDIA[type]) {
		return {
			kind: MEDIA[type],
			text: node.caption ?? undefined,
			media: {
				mimetype: node.mimetype ?? undefined,
				size: node.fileLength ? toNumber(node.fileLength) : undefined,
				file_name: node.fileName ?? undefined,
				seconds: node.seconds ?? undefined,
				voice: node.ptt ?? undefined,
			},
		};
	}
	const loc = c.locationMessage ?? c.liveLocationMessage;
	if (loc) {
		const place = [c.locationMessage?.name, c.locationMessage?.address].filter(Boolean).join(', ');
		return { kind: 'location', text: `${loc.degreesLatitude},${loc.degreesLongitude}${place ? ` (${place})` : ''}` };
	}
	if (c.contactMessage) return { kind: 'contact', text: c.contactMessage.vcard ?? c.contactMessage.displayName ?? '' };
	const poll = c.pollCreationMessage ?? c.pollCreationMessageV2 ?? c.pollCreationMessageV3;
	if (poll) {
		const options = (poll.options ?? []).map((o) => `- ${o.optionName}`).join('\n');
		return { kind: 'poll', text: `${poll.name ?? ''}\n${options}` };
	}
	return { kind: 'unsupported', text: type };
}

function textOf(c: proto.IMessage | undefined): string {
	if (!c) return '';
	if (c.conversation) return c.conversation;
	if (c.extendedTextMessage?.text) return c.extendedTextMessage.text;
	const type = getContentType(c);
	const node = (type ? c[type] : undefined) as Node | undefined;
	const caption = node?.caption ? ` ${node.caption}` : '';
	return type && MEDIA[type] ? `[${MEDIA[type]}]${caption}` : '';
}

function excerpt(s: string): string {
	return s.length > 300 ? s.slice(0, 300) + '…' : s;
}

// --- outgoing messages ---

function quoting(replyTo: unknown): MiscMessageGenerationOptions | undefined {
	if (!replyTo) return undefined;
	const quoted = recent.get(String(replyTo));
	if (!quoted) throw new Error(`message ${replyTo} is not among the recent messages`);
	return { quoted };
}

async function send(chat: unknown, content: AnyMessageContent, options?: MiscMessageGenerationOptions) {
	const sent = await sock.sendMessage(String(chat), content, options);
	remember(sent);
	return { id: sent?.key.id };
}

// Group metadata, cached for a minute: mentions need the member list.
const groupMeta = new Map<string, { at: number; md: GroupMetadata }>();

async function metadata(jid: string): Promise<GroupMetadata> {
	const hit = groupMeta.get(jid);
	if (hit && Date.now() - hit.at < 60_000) return hit.md;
	const md = await sock.groupMetadata(jid);
	groupMeta.set(jid, { at: Date.now(), md });
	return md;
}

// mentionsFor turns phone-number JIDs into the JIDs a group knows its members by (@lid in groups
// that hide numbers) and rewrites their @tokens in the text to match, so the mentions render. all
// adds every other member: they all get notified without a token each.
async function mentionsFor(chat: string, text: string, wanted: string[], all: boolean) {
	if (!isJidGroup(chat)) return { text, jids: wanted };
	const md = await metadata(chat);
	const jids = new Set<string>();
	for (const pn of wanted) {
		const p = md.participants.find((p) => jidNormalizedUser(p.id) === pn || (p.phoneNumber && jidNormalizedUser(p.phoneNumber) === pn));
		const id = p ? jidNormalizedUser(p.id) : pn;
		if (id !== pn) text = text.replaceAll('@' + userOf(pn), '@' + userOf(id));
		jids.add(id);
	}
	if (all) {
		const me = [jidNormalizedUser(sock.user?.id), sock.user?.lid ? jidNormalizedUser(sock.user.lid) : ''];
		for (const p of md.participants) {
			const id = jidNormalizedUser(p.id);
			if (!me.includes(id)) jids.add(id);
		}
	}
	return { text, jids: [...jids] };
}

// WhatsApp stickers are 512×512 WebP: anything else is letterboxed onto a transparent square.
function sticker(path: string): Promise<Buffer> {
	return sharp(path, { animated: true })
		.resize(512, 512, { fit: 'contain', background: { r: 0, g: 0, b: 0, alpha: 0 } })
		.webp({ quality: 80 })
		.toBuffer();
}

// --- requests ---

const methods: Record<string, (params: Json) => Promise<unknown>> = {
	// Text, optionally quoting a recent message and @mentioning people (phone-number JIDs) or everyone.
	async send_text({ chat, text, reply_to, mentions, mention_all }) {
		const wanted = Array.isArray(mentions) ? mentions.map(String) : [];
		const m = await mentionsFor(String(chat), String(text), wanted, Boolean(mention_all));
		return send(chat, m.jids.length ? { text: m.text, mentions: m.jids } : { text: m.text }, quoting(reply_to));
	},
	// A local file sent as kind: image | video | audio | voice | document | sticker.
	async send_media({ chat, path, kind, mimetype, caption, file_name, reply_to }) {
		const file = { url: String(path) };
		const type = mimetype ? String(mimetype) : undefined;
		const cap = caption ? { caption: String(caption) } : {};
		let content: AnyMessageContent;
		switch (kind) {
			case 'image':
				content = { image: file, mimetype: type, ...cap };
				break;
			case 'video':
				content = { video: file, mimetype: type, ...cap };
				break;
			case 'audio': // the real mimetype, or Baileys labels every clip as Opus
				content = { audio: file, mimetype: type };
				break;
			case 'voice':
				content = { audio: file, mimetype: 'audio/ogg; codecs=opus', ptt: true };
				break;
			case 'sticker':
				content = { sticker: await sticker(String(path)) };
				break;
			default:
				content = {
					document: file,
					mimetype: type ?? 'application/octet-stream',
					fileName: file_name ? String(file_name) : basename(String(path)),
					...cap,
				};
		}
		return send(chat, content, quoting(reply_to));
	},
	// A reaction on a recent message; an empty emoji takes ours off.
	async react({ id, emoji }) {
		const target = recent.get(String(id));
		if (!target?.key.remoteJid) throw new Error(`message ${id} is not among the recent messages`);
		return send(target.key.remoteJid, { react: { text: String(emoji ?? ''), key: target.key } });
	},
	// The groups this account is in. People need no listing: their phone number is their JID.
	async chats() {
		const groups = await sock.groupFetchAllParticipating();
		return {
			groups: Object.values(groups).map((g) => ({ jid: g.id, name: g.subject ?? '', size: g.participants?.length ?? 0 })),
		};
	},
	// Who is in a chat: a group's name, description and members, or a person's name.
	async chat_info({ chat }) {
		const jid = String(chat);
		if (!isJidGroup(jid)) return { jid, name: names.get(jid) };
		groupMeta.delete(jid); // always fresh
		const md = await metadata(jid);
		const members = [];
		for (const p of md.participants) {
			const pn = p.phoneNumber ? jidNormalizedUser(p.phoneNumber) : await phoneJid(p.id, undefined);
			members.push({ jid: pn, name: names.get(pn), admin: p.admin ?? undefined });
		}
		return { jid, name: md.subject, description: md.desc, members };
	},
	// Save the media of a recent message to a local file.
	async download({ id, path }) {
		const m = recent.get(String(id));
		if (!m) throw new Error(`message ${id} is not among the recent messages`);
		const stream = await downloadMediaMessage(m, 'stream', {}, { logger, reuploadRequest: sock.updateMediaMessage });
		await pipeline(stream, createWriteStream(String(path), { mode: 0o600 }));
		return { path };
	},
	async logout() {
		stopping = true;
		await sock.logout();
		return {};
	},
};

const rl = createInterface({ input: process.stdin });
rl.on('line', (line) => {
	let req: { id?: number; method?: string; params?: Json };
	try {
		req = JSON.parse(line);
	} catch {
		return;
	}
	const handler = methods[req.method ?? ''];
	const run = handler ? handler(req.params ?? {}) : Promise.reject(new Error(`unknown method ${req.method}`));
	run.then(
		(result) => emit({ type: 'response', id: req.id, result: result ?? {} }),
		(err: unknown) => emit({ type: 'response', id: req.id, error: err instanceof Error ? err.message : String(err) }),
	);
});
// The Go side closes our stdin to stop us.
rl.on('close', () => {
	stopping = true;
	saveNames();
	void sock.end(undefined);
	setTimeout(() => process.exit(0), 500);
});

connect();
