/** "3m ago" / "in 2h" style relative time for an ISO timestamp. */
export function relativeTime(iso: string | undefined | null): string {
	if (!iso) return '—';
	const t = Date.parse(iso);
	if (Number.isNaN(t)) return iso;
	const diffSec = (t - Date.now()) / 1000;
	const abs = Math.abs(diffSec);
	if (abs < 5) return 'just now';
	let label: string;
	if (abs < 60) label = `${Math.round(abs)}s`;
	else if (abs < 3600) label = `${Math.round(abs / 60)}m`;
	else if (abs < 86400) label = `${Math.round(abs / 3600)}h`;
	else label = `${Math.round(abs / 86400)}d`;
	return diffSec < 0 ? `${label} ago` : `in ${label}`;
}

/** Full local timestamp, used for tooltips. */
export function absoluteTime(iso: string | undefined | null): string {
	if (!iso) return '';
	const d = new Date(iso);
	if (Number.isNaN(d.getTime())) return iso;
	return d.toLocaleString(undefined, {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit'
	});
}

/** Compact duration from seconds: "42s", "5m 12s", "2h 5m", "3d 4h". */
export function duration(seconds: number | undefined | null): string {
	if (seconds == null || Number.isNaN(seconds)) return '—';
	if (seconds < 0) seconds = 0;
	if (seconds < 10) {
		const v = Math.round(seconds * 10) / 10;
		return `${Number.isInteger(v) ? v.toFixed(0) : v.toFixed(1)}s`;
	}
	if (seconds < 60) return `${Math.round(seconds)}s`;
	const s = Math.round(seconds);
	const d = Math.floor(s / 86400);
	const h = Math.floor((s % 86400) / 3600);
	const m = Math.floor((s % 3600) / 60);
	const sec = s % 60;
	if (d > 0) return h > 0 ? `${d}d ${h}h` : `${d}d`;
	if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
	return sec > 0 ? `${m}m ${sec}s` : `${m}m`;
}

/** "123 ms" / "1.24 s" from milliseconds. */
export function millis(ms: number | undefined | null): string {
	if (ms == null || Number.isNaN(ms)) return '—';
	return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(2)} s`;
}

/** Locale-formatted integer, em dash when missing. */
export function num(n: number | undefined | null): string {
	return n == null ? '—' : n.toLocaleString();
}

/** Truncate long IDs in the middle: "0198c0de-4f…9b7f3a2b". */
export function truncateMiddle(s: string, max = 24): string {
	if (s.length <= max) return s;
	const keep = max - 1;
	const front = Math.ceil(keep * 0.6);
	const back = keep - front;
	return `${s.slice(0, front)}…${s.slice(s.length - back)}`;
}

export interface BodyPreview {
	kind: 'json' | 'text' | 'binary' | 'empty';
	text?: string;
	bytes: number;
}

/** Decode a base64 request body into a printable preview (pretty JSON when possible). */
export function decodeBody(b64: string | undefined | null): BodyPreview {
	if (!b64) return { kind: 'empty', bytes: 0 };
	let bytes: Uint8Array;
	try {
		const raw = atob(b64);
		bytes = new Uint8Array(raw.length);
		for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
	} catch {
		return { kind: 'binary', bytes: Math.floor((b64.length * 3) / 4) };
	}
	try {
		const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
		const trimmed = text.trim();
		if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
			try {
				return {
					kind: 'json',
					text: JSON.stringify(JSON.parse(text), null, 2),
					bytes: bytes.length
				};
			} catch {
				/* fall through to plain text */
			}
		}
		return { kind: 'text', text, bytes: bytes.length };
	} catch {
		return { kind: 'binary', bytes: bytes.length };
	}
}
