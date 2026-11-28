<script lang="ts">
	import '@fontsource-variable/geist';
	import '@fontsource-variable/geist-mono';
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';
	import { apps } from '$lib/apps.svelte';
	import AppRail from '$lib/components/apps/AppRail.svelte';
	import { errorMessage } from '$lib/api';
	import TopBar from '$lib/components/TopBar.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';

	let { children }: { children: Snippet } = $props();

	let loadError = $state<string | null>(null);

	// Pages reachable without being signed in.
	const route = $derived(page.url.pathname.slice(base.length) || '/');
	const isSetup = $derived(route.startsWith('/setup'));
	const isLogin = $derived(route.startsWith('/login'));
	const isInvite = $derived(route.startsWith('/invite'));
	const isPublic = $derived(isSetup || isLogin || isInvite);
	const isWelcome = $derived(route.startsWith('/welcome'));

	$effect(() => {
		auth.refresh().catch((e) => (loadError = errorMessage(e)));
	});

	/** Where the guard wants us to be, or null to stay. */
	const redirect = $derived.by(() => {
		const s = auth.session;
		if (!s) return null;
		if (s.setup_required) return isSetup || isInvite ? null : `${base}/setup`;
		if (isSetup) return `${base}/`;
		if (!s.authenticated) {
			if (isPublic) return null;
			const next = page.url.pathname + page.url.search;
			return `${base}/login?next=${encodeURIComponent(next)}`;
		}
		// SaaS: a signed-in user with no team (new, or removed from theirs)
		// must create or pick one before anything else.
		if (s.mode === 'saas' && s.needs_team && !isWelcome && !route.startsWith('/settings/account')) {
			return `${base}/welcome`;
		}
		if (isLogin) return safeNext(page.url.searchParams.get('next'));
		return null;
	});

	/** Only follow same-app relative paths after login (no open redirects). */
	function safeNext(next: string | null): string {
		if (next && next.startsWith(`${base}/`) && !next.startsWith('//')) return next;
		return `${base}/`;
	}

	$effect(() => {
		if (redirect) goto(redirect, { replaceState: true });
	});

	const ready = $derived(auth.session !== null && redirect === null);
	const bare = $derived(isPublic || !auth.session?.authenticated);

	// The applications of whoever is signed in: reloaded on sign-in, team
	// switches and API key changes.
	const appsKey = $derived.by(() => {
		const s = auth.session;
		if (!s?.authenticated || !s.permissions.read || (s.mode === 'saas' && !s.team)) return '';
		return `${s.kind}|${s.user?.id ?? ''}|${s.team?.id ?? ''}|${auth.key}`;
	});
	$effect(() => {
		if (appsKey) apps.load();
		else apps.reset();
	});
	// The rail carries navigation and the account menu, so it shows whenever
	// someone is signed in (its application list may still be empty).
	const showRail = $derived(ready && !bare);
</script>

<svelte:head>
	<title>pushq</title>
</svelte:head>

<div class="flex min-h-screen">
	{#if showRail}
		<!-- Above the sticky top bar (z-30), so the rail's name tags show over it. -->
		<div class="sticky top-0 z-40 h-screen">
			<AppRail />
		</div>
	{/if}
	<div class="flex min-h-screen min-w-0 flex-1 flex-col">
		<TopBar />
		<main class="mx-auto w-full max-w-5xl flex-1 px-4 py-10 sm:px-6">
			{#if loadError && !auth.session}
				<ErrorAlert message={`Can't reach the pushq server: ${loadError}`} />
			{:else if ready}
				{@render children()}
			{:else}
				<p class="py-16 text-center text-sm text-muted-foreground">Loading…</p>
			{/if}
		</main>
	</div>
</div>
