import { base } from '$app/paths';
import type { Queue } from '$lib/api';

/**
 * Queue names are '/'-separated paths: "user/instagram" is the queue
 * "instagram" inside the folder "user". Folders have no server-side existence
 * of their own — they are derived from the names of the queues in them.
 */

/** Parent folder of a queue name ("" for top-level queues). */
export function folderOf(name: string): string {
	const i = name.lastIndexOf('/');
	return i < 0 ? '' : name.slice(0, i);
}

/** Last path segment of a queue or folder name. */
export function leafOf(name: string): string {
	return name.slice(name.lastIndexOf('/') + 1);
}

/** Dashboard URL for a folder listing ("" = all queues). */
export function folderHref(folder: string): string {
	return folder ? `${base}/?folder=${encodeURIComponent(folder)}` : `${base}/`;
}

/** Dashboard URL for a queue page. The name travels as one segment (%2F). */
export function queueHref(name: string): string {
	return `${base}/queues/${encodeURIComponent(name)}`;
}

export interface Crumb {
	label: string;
	/** Full folder path this crumb links to ("" = root). */
	path: string;
}

/** Breadcrumbs for a folder path: "a/b" -> [a, a/b]. */
export function crumbs(folder: string): Crumb[] {
	if (!folder) return [];
	const parts = folder.split('/');
	return parts.map((label, i) => ({ label, path: parts.slice(0, i + 1).join('/') }));
}

export interface FolderRow {
	kind: 'folder';
	/** Full folder path, e.g. "user/instagram". */
	path: string;
	label: string;
	queues: number;
	paused: number;
	disabled: number;
}

export interface QueueRow {
	kind: 'queue';
	label: string;
	queue: Queue;
}

export type Row = FolderRow | QueueRow;

/**
 * Splits queues (all under `folder`) into immediate subfolders and the queues
 * that sit directly in `folder`. A name can be both a queue and a folder
 * ("user" and "user/instagram"); both rows are shown.
 */
export function groupByFolder(queues: Queue[], folder: string): Row[] {
	const prefix = folder ? folder + '/' : '';
	const folders = new Map<string, FolderRow>();
	const direct: QueueRow[] = [];
	for (const q of queues) {
		if (!q.name.startsWith(prefix)) continue;
		const rest = q.name.slice(prefix.length);
		const slash = rest.indexOf('/');
		if (slash < 0) {
			direct.push({ kind: 'queue', label: rest, queue: q });
			continue;
		}
		const label = rest.slice(0, slash);
		const path = prefix + label;
		let f = folders.get(path);
		if (!f) {
			f = { kind: 'folder', path, label, queues: 0, paused: 0, disabled: 0 };
			folders.set(path, f);
		}
		f.queues++;
		if (q.state === 'paused') f.paused++;
		if (q.state === 'disabled') f.disabled++;
	}
	const byLabel = (a: { label: string }, b: { label: string }) =>
		a.label < b.label ? -1 : a.label > b.label ? 1 : 0;
	return [...[...folders.values()].sort(byLabel), ...direct.sort(byLabel)];
}
