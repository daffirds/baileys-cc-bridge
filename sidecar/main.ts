// The WhatsApp side of bailey-cc-bridge. The Go daemon runs this file with node and talks to it in
// JSON lines: requests on stdin, responses and events on stdout. It holds the Baileys connection and
// nothing else. The whitelist, the sessions and the agents all live on the Go side.
//
// Events:    {"type":"qr","qr":"…"}  {"type":"pairing_code","code":"…"}
//            {"type":"ready","me":{"id":"…","lid":"…","name":"…"}}  {"type":"closed","reason":"…"}
// Requests:  {"id":1,"method":"logout","params":{}}
// Responses: {"type":"response","id":1,"result":{…}}  or  {"type":"response","id":1,"error":"…"}

import { createInterface } from 'node:readline';
import { format, parseArgs } from 'node:util';
import makeWASocket, {
	Browsers,
	DisconnectReason,
	fetchLatestBaileysVersion,
	jidNormalizedUser,
	makeCacheableSignalKeyStore,
	useMultiFileAuthState,
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

const methods: Record<string, (params: Json) => Promise<unknown>> = {
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
