import { appContext } from '$lib/app-context.svelte';
import { appsApi, type App } from '$lib/api';

/**
 * The applications of the current team (self-hosted: of the server) and the
 * one the console is showing. Every queue belongs to one application; the
 * app rail switches between them.
 */
class Apps {
	list = $state<App[] | null>(null);
	error = $state<string | null>(null);

	/** The application on screen (the Default one until the list loads). */
	get current(): App | undefined {
		const l = this.list;
		if (!l) return undefined;
		return l.find((a) => a.id === appContext.id) ?? l.find((a) => a.default) ?? l[0];
	}

	/** Re-reads the list and makes sure the selection still exists. */
	async load(): Promise<App[]> {
		try {
			const { apps } = await appsApi.list();
			this.list = apps;
			this.error = null;
			const cur = this.current;
			if (cur && cur.id !== appContext.id) appContext.set(cur.id);
			return apps;
		} catch (e) {
			this.list = [];
			this.error = e instanceof Error ? e.message : String(e);
			return [];
		}
	}

	select(id: number) {
		appContext.set(id);
	}

	reset() {
		this.list = null;
	}
}

export const apps = new Apps();

/** Two-letter initials for an application's avatar. */
export function appInitials(name: string): string {
	const words = name.trim().split(/[\s\-_/.]+/).filter(Boolean);
	if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
	return (words[0] ?? '?').slice(0, 2).toUpperCase();
}

const HUES = [210, 262, 152, 24, 330, 190, 45, 290];

/** A stable avatar colour per application. */
export function appHue(id: number): number {
	return HUES[id % HUES.length];
}
