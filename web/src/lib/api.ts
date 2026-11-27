import { auth, type Role, type User } from '$lib/auth.svelte';

/** RFC 9457 problem+json body. */
export interface Problem {
	type?: string;
	title?: string;
	status?: number;
	code?: string;
	detail?: string;
	instance?: string;
}

export class ApiError extends Error {
	status: number;
	code?: string;
	detail?: string;

	constructor(status: number, problem: Problem) {
		super(problem.detail || problem.title || problem.code || `HTTP ${status}`);
		this.name = 'ApiError';
		this.status = status;
		this.code = problem.code;
		this.detail = problem.detail;
	}
}

export function errorMessage(e: unknown): string {
	if (e instanceof ApiError) return e.message;
	if (e instanceof Error) return e.message || 'Request failed';
	return String(e);
}

export interface RateLimits {
	max_dispatches_per_second?: number;
	max_burst_size?: number;
	max_concurrent_dispatches?: number;
}

export interface Retry {
	max_attempts?: number;
	max_retry_duration_seconds?: number;
	min_backoff_seconds?: number;
	max_backoff_seconds?: number;
	max_doublings?: number;
}

export interface Target {
	base_url?: string;
	method?: string;
	headers?: Record<string, string>;
	has_signing_secret?: boolean;
}

export type QueueState = 'running' | 'paused' | 'disabled';

export interface Queue {
	name: string;
	state?: QueueState;
	rate_limits?: RateLimits;
	retry?: Retry;
	target?: Target;
	dead_letter_queue?: string;
	purged_at?: string;
	created_at?: string;
	updated_at?: string;
}

export interface QueueList {
	queues?: Queue[];
	next_page_token?: string;
}

export interface QueueStats {
	state?: string;
	pending?: number;
	running?: number;
	succeeded?: number;
	failed?: number;
	scheduled_future?: number;
	oldest_due_at?: string;
	oldest_due_age_seconds?: number;
	dispatched_last_1m?: number;
	errors_last_1m?: number;
	dispatched_last_1h?: number;
	errors_last_1h?: number;
	last_purge?: string;
}

export type TaskState = 'pending' | 'running' | 'succeeded' | 'failed';

export interface LastAttempt {
	at?: string;
	status?: number;
	error?: string;
}

export interface Task {
	id: string;
	queue?: string;
	state?: TaskState;
	url?: string;
	method?: string;
	headers?: Record<string, string>;
	body_base64?: string;
	schedule_time?: string;
	created_at?: string;
	dispatch_deadline_seconds?: number;
	retry?: Retry;
	dispatch_count?: number;
	response_count?: number;
	first_attempt_at?: string;
	finished_at?: string;
	last_attempt?: LastAttempt;
}

export interface TaskList {
	tasks?: Task[];
	next_page_token?: string;
}

export interface Attempt {
	number?: number;
	dispatch_time?: string;
	response_time?: string;
	response_status?: number;
	error_kind?: string;
	error_snippet?: string;
	duration_ms?: number;
}

export interface AttemptList {
	attempts?: Attempt[];
}

export interface Ready {
	role?: string;
	dialect?: string;
}

function qs(params: Record<string, string | number | undefined>): string {
	const sp = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) {
		if (v !== undefined && v !== '') sp.set(k, String(v));
	}
	const s = sp.toString();
	return s ? `?${s}` : '';
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
	const headers = new Headers(init.headers);
	headers.set('Accept', 'application/json, application/problem+json');
	// Required by the server for cookie-authenticated writes (CSRF guard).
	headers.set('X-Requested-With', 'pushq');
	if (init.body !== undefined) headers.set('Content-Type', 'application/json');
	if (auth.key) headers.set('Authorization', `Bearer ${auth.key}`);
	const res = await fetch(`${window.location.origin}${path}`, { ...init, headers });
	let data: unknown;
	const text = await res.text();
	if (text) {
		try {
			data = JSON.parse(text);
		} catch {
			data = undefined;
		}
	}
	if (!res.ok) {
		if (res.status === 401 && !path.startsWith('/v1/auth/')) auth.onUnauthorized();
		throw new ApiError(res.status, (data ?? {}) as Problem);
	}
	return data as T;
}

const enc = encodeURIComponent;

const json = (method: string, body?: unknown): RequestInit => ({
	method,
	body: body === undefined ? undefined : JSON.stringify(body)
});

export type KeyRole = 'admin' | 'writer';

export interface Invite {
	id: number;
	kind: 'invite' | 'reset';
	email: string;
	role?: Role;
	created_at: string;
	expires_at: string;
}

export interface ApiKey {
	id: number;
	name: string;
	prefix: string;
	role: KeyRole;
	created_by?: string;
	created_at: string;
	last_used_at?: string;
}

/** Turns a server-relative "/invite#token=…" path into a full link. */
export function absoluteLink(path: string): string {
	return `${window.location.origin}${path}`;
}

/** "Continue with GitHub": a full-page navigation to GitHub's consent screen. */
export function githubLoginUrl(next?: string): string {
	const q = new URLSearchParams({ provider: 'github' });
	if (next) q.set('next', next);
	return `${window.location.origin}/v1/auth/workos/login?${q}`;
}

/** A sign-in WorkOS paused until the person enters the verification code it emailed. */
export const emailVerifyApi = {
	pending: () => request<{ pending: boolean; email?: string }>('/v1/auth/email-verification'),
	submit: (code: string) =>
		request<{ user: User; next?: string }>('/v1/auth/email-verification', json('POST', { code }))
};

/** SaaS passwordless sign-in: email a 6-digit code, then sign in with it. */
export const emailCodeApi = {
	send: (email: string) => request<{ sent: boolean }>('/v1/auth/email-code', json('POST', { email })),
	verify: (email: string, code: string) =>
		request<{ user: User }>('/v1/auth/email-code:verify', json('POST', { email, code }))
};

export const teamsApi = {
	list: () => request<{ teams: { id: number; name: string; role: Role }[] }>('/v1/teams'),
	create: (name: string) => request<{ id: number; name: string; role: Role }>('/v1/teams', json('POST', { name })),
	switchTo: (id: number) => request<void>(`/v1/teams/${id}:switch`, { method: 'POST' }),
	renameCurrent: (name: string) => request<{ id: number; name: string }>('/v1/teams/current', json('PATCH', { name }))
};

export const accountsApi = {
	setup: (body: { email: string; name: string; password: string; api_key?: string }) =>
		request<{ user: User }>('/v1/auth/setup', json('POST', body)),
	login: (email: string, password: string) =>
		request<{ user: User }>('/v1/auth/login', json('POST', { email, password })),
	getInvite: (token: string) => request<Invite>(`/v1/auth/invites/${enc(token)}`),
	acceptInvite: (token: string, name: string, password: string) =>
		request<{ user: User }>(`/v1/auth/invites/${enc(token)}:accept`, json('POST', { name, password })),

	updateMe: (name: string) => request<User>('/v1/me', json('PATCH', { name })),
	changePassword: (current_password: string, new_password: string) =>
		request<void>('/v1/me/password', json('POST', { current_password, new_password })),

	listUsers: () => request<{ users: User[] }>('/v1/users'),
	updateUser: (id: number, patch: { role?: Role; name?: string }) =>
		request<User>(`/v1/users/${id}`, json('PATCH', patch)),
	deleteUser: (id: number) => request<void>(`/v1/users/${id}`, { method: 'DELETE' }),
	resetLink: (id: number) =>
		request<{ link: Invite; path: string }>(`/v1/users/${id}/reset-link`, { method: 'POST' }),

	listInvites: () => request<{ invites: Invite[] }>('/v1/invites'),
	/** Self-hosted: returns a link to share. SaaS: WorkOS emails it (emailed: true). */
	createInvite: (email: string, role: Role) =>
		request<{ invite: Invite; path?: string; emailed?: boolean }>('/v1/invites', json('POST', { email, role })),
	deleteInvite: (id: number) => request<void>(`/v1/invites/${id}`, { method: 'DELETE' }),

	listKeys: () => request<{ keys: ApiKey[]; config_keys: number }>('/v1/api-keys'),
	createKey: (name: string, role: KeyRole) =>
		request<{ key: ApiKey; secret: string }>('/v1/api-keys', json('POST', { name, role })),
	deleteKey: (id: number) => request<void>(`/v1/api-keys/${id}`, { method: 'DELETE' })
};

export const api = {
	listQueues: (opts: { namePrefix?: string; pageSize?: number; pageToken?: string } = {}) =>
		request<QueueList>(
			`/v1/queues${qs({
				name_prefix: opts.namePrefix,
				page_size: opts.pageSize ?? 100,
				page_token: opts.pageToken
			})}`
		),

	getQueue: (queue: string) => request<Queue>(`/v1/queues/${enc(queue)}`),

	getQueueStats: (queue: string) => request<QueueStats>(`/v1/queues/${enc(queue)}/stats`),

	pauseQueue: (queue: string) => request<Queue>(`/v1/queues/${enc(queue)}:pause`, { method: 'POST' }),

	resumeQueue: (queue: string) =>
		request<Queue>(`/v1/queues/${enc(queue)}:resume`, { method: 'POST' }),

	purgeQueue: (queue: string) => request<Queue>(`/v1/queues/${enc(queue)}:purge`, { method: 'POST' }),

	listTasks: (queue: string, opts: { state?: string; pageSize?: number; pageToken?: string } = {}) =>
		request<TaskList>(
			`/v1/queues/${enc(queue)}/tasks${qs({
				state: opts.state,
				page_size: opts.pageSize ?? 50,
				page_token: opts.pageToken
			})}`
		),

	getTask: (queue: string, id: string, view: 'basic' | 'full' = 'full') =>
		request<Task>(`/v1/queues/${enc(queue)}/tasks/${enc(id)}${qs({ view })}`),

	deleteTask: (queue: string, id: string) =>
		request<void>(`/v1/queues/${enc(queue)}/tasks/${enc(id)}`, { method: 'DELETE' }),

	runTask: (queue: string, id: string) =>
		request<Task>(`/v1/queues/${enc(queue)}/tasks/${enc(id)}:run`, { method: 'POST' }),

	listAttempts: (queue: string, id: string) =>
		request<AttemptList>(`/v1/queues/${enc(queue)}/tasks/${enc(id)}/attempts`)
};

/** /readyz is unauthenticated — fetched without the Authorization header. */
export async function getReady(): Promise<Ready> {
	const res = await fetch(`${window.location.origin}/readyz`, {
		headers: { Accept: 'application/json' }
	});
	if (!res.ok) throw new Error(`readyz returned ${res.status}`);
	return (await res.json()) as Ready;
}
