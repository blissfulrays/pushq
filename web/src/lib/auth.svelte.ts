import { browser } from '$app/environment';

const STORAGE_KEY = 'pushq_api_key';

class Auth {
	/** Current API key ('' = none; the server may be running unauthenticated). */
	key = $state(browser ? (sessionStorage.getItem(STORAGE_KEY) ?? '') : '');
	/** Whether the key-entry dialog is open (opened on demand or after a 401). */
	promptOpen = $state(false);

	setKey(value: string) {
		this.key = value.trim();
		if (browser) {
			try {
				if (this.key) sessionStorage.setItem(STORAGE_KEY, this.key);
				else sessionStorage.removeItem(STORAGE_KEY);
			} catch {
				/* sessionStorage unavailable */
			}
		}
		this.promptOpen = false;
	}

	requireKey() {
		this.promptOpen = true;
	}
}

export const auth = new Auth();
