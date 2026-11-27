import { browser } from '$app/environment';

const KEY_STORAGE = 'pushq_api_key';

export type Role = 'admin' | 'member' | 'viewer';

export interface User {
	id: number;
	email: string;
	name: string;
	role: Role;
	created_at: string;
	last_login_at?: string;
}

export interface Permissions {
	read: boolean;
	list: boolean;
	write: boolean;
	delete_queue: boolean;
	manage: boolean;
}

export interface TeamInfo {
	id: number;
	name: string;
	role?: Role;
}

/** GET /v1/auth/session */
export interface SessionInfo {
	/**
	 * dev: auth disabled · open: no credentials needed yet · locked: sign-in
	 * required · saas: WorkOS sign-in, one isolated workspace per team
	 */
	mode: 'dev' | 'open' | 'locked' | 'saas';
	setup_required: boolean;
	setup_requires_key: boolean;
	authenticated: boolean;
	kind?: 'open' | 'api_key' | 'user';
	role?: string;
	user?: User;
	key_name?: string;
	permissions: Permissions;
	sso_providers?: string[];
	/** SaaS: current team, the user's teams, and whether they must pick/create one. */
	team?: TeamInfo;
	teams?: TeamInfo[];
	needs_team?: boolean;
}

const noPerms: Permissions = { read: false, list: false, write: false, delete_queue: false, manage: false };

function readStoredKey(): string {
	if (!browser) return '';
	try {
		return sessionStorage.getItem(KEY_STORAGE) ?? '';
	} catch {
		return '';
	}
}

class Auth {
	/** Latest session info; null until the first check completes. */
	session = $state<SessionInfo | null>(null);
	/** Optional API key used instead of a login (kept for this tab only). */
	key = $state(readStoredKey());

	get can(): Permissions {
		return this.session?.permissions ?? noPerms;
	}

	get user(): User | undefined {
		return this.session?.user;
	}

	get saas(): boolean {
		return this.session?.mode === 'saas';
	}

	/** Re-reads who we are. Called at startup, after sign-in/out and on 401s. */
	async refresh(): Promise<SessionInfo> {
		const headers: Record<string, string> = { Accept: 'application/json' };
		if (this.key) headers.Authorization = `Bearer ${this.key}`;
		const res = await fetch(`${window.location.origin}/v1/auth/session`, { headers });
		if (!res.ok) throw new Error(`session check failed (HTTP ${res.status})`);
		const s = (await res.json()) as SessionInfo;
		this.session = s;
		return s;
	}

	setKey(value: string) {
		this.key = value.trim();
		try {
			if (this.key) sessionStorage.setItem(KEY_STORAGE, this.key);
			else sessionStorage.removeItem(KEY_STORAGE);
		} catch {
			/* sessionStorage unavailable */
		}
	}

	async signOut() {
		this.setKey('');
		await fetch(`${window.location.origin}/v1/auth/logout`, {
			method: 'POST',
			headers: { 'X-Requested-With': 'pushq' }
		}).catch(() => {});
		await this.refresh().catch(() => {});
	}

	private refreshing: Promise<unknown> | null = null;

	/** A request came back 401: our session ended. Re-check (once at a time);
	 * the layout then routes to the login page. */
	onUnauthorized() {
		if (!this.refreshing) {
			this.refreshing = this.refresh()
				.catch(() => {})
				.finally(() => (this.refreshing = null));
		}
	}
}

export const auth = new Auth();
