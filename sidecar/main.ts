// The WhatsApp side of bailey-cc-bridge. The Go daemon runs this file with node and talks to it in
// JSON lines: requests on stdin, responses and events on stdout. It holds the Baileys connection and
// nothing else. The whitelist, the sessions and the agents all live on the Go side.
//
// Events:    {"type":"qr","qr":"…"}  {"type":"pairing_code","code":"…"}
//            {"type":"ready","me":{"id":"…","lid":"…","name":"…"}}  {"type":"closed","reason":"…"}
//            {"type":"message","message":{…}}  (see Incoming)
// Requests:  {"id":1,"method":"chats","params":{}}  (methods: chats, download, logout)
// Responses: {"type":"response","id":1,"result":{…}}  or  {"type":"response","id":1,"error":"…"}

import { createWriteStream } from 'node:fs';
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
	type WAMessage,
	type WASocket,
} from 'baileys';
import pino from 'pino';

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
		// re-established is dropped instead of retried.
		getMessage: async () => ({ conversation: '' }),
	});
	sock.ev.on('creds.update', saveCreds);
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
	quoted?: { id: string; sender?: string; text?: string };
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

// Message types that carry nothing worth telling an agent.
const IGNORED = new Set([
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
	const ctx = node.contextInfo;
	const quoted = ctx?.stanzaId
		? {
				id: ctx.stanzaId,
				sender: ctx.participant ? jidNormalizedUser(ctx.participant) : undefined,
				text: excerpt(textOf(normalizeMessageContent(ctx.quotedMessage))),
			}
		: undefined;
	if (type === 'conversation') return { kind: 'text', text: c.conversation ?? '' };
	if (type === 'extendedTextMessage') return { kind: 'text', text: c.extendedTextMessage?.text ?? '', quoted };
	if (MEDIA[type]) {
		return {
			kind: MEDIA[type],
			text: node.caption ?? undefined,
			quoted,
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
		return { kind: 'location', text: `${loc.degreesLatitude},${loc.degreesLongitude}${place ? ` (${place})` : ''}`, quoted };
	}
	if (c.contactMessage) return { kind: 'contact', text: c.contactMessage.vcard ?? c.contactMessage.displayName ?? '', quoted };
	const poll = c.pollCreationMessage ?? c.pollCreationMessageV2 ?? c.pollCreationMessageV3;
	if (poll) {
		const options = (poll.options ?? []).map((o) => `- ${o.optionName}`).join('\n');
		return { kind: 'poll', text: `${poll.name ?? ''}\n${options}`, quoted };
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

// --- requests ---

const methods: Record<string, (params: Json) => Promise<unknown>> = {
	// The groups this account is in. People need no listing: their phone number is their JID.
	async chats() {
		const groups = await sock.groupFetchAllParticipating();
		return {
			groups: Object.values(groups).map((g) => ({ jid: g.id, name: g.subject ?? '', size: g.participants?.length ?? 0 })),
		};
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
	void sock.end(undefined);
	setTimeout(() => process.exit(0), 500);
});

connect();
