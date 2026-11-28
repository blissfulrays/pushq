import { browser } from '$app/environment';

const STORAGE = 'pushq_app';

function readStored(): number | null {
	if (!browser) return null;
	try {
		const v = Number(localStorage.getItem(STORAGE));
		return Number.isInteger(v) && v > 0 ? v : null;
	} catch {
		return null;
	}
}

/**
 * The application queue requests act in. Kept apart from the apps store so
 * the API client can read it without an import cycle; it's sent as the
 * X-PushQ-App header on queue and task requests.
 */
class AppContext {
	/** Selected application id; null = the server's default. */
	id = $state<number | null>(readStored());

	set(id: number | null) {
		this.id = id;
		try {
			if (id) localStorage.setItem(STORAGE, String(id));
			else localStorage.removeItem(STORAGE);
		} catch {
			/* storage unavailable */
		}
	}
}

export const appContext = new AppContext();
