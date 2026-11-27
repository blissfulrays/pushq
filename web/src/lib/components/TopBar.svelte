<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { getReady, type Ready } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import ThemeToggle from './ThemeToggle.svelte';

	let { showAccount = true }: { showAccount?: boolean } = $props();

	let ready = $state<Ready | null>(null);
	let menuOpen = $state(false);
	let menuEl = $state<HTMLElement | null>(null);

	const s = $derived(auth.session);
	const label = $derived.by(() => {
		if (!s) return '';
		if (s.user) return s.user.name || s.user.email;
		if (s.kind === 'api_key') return `API key · ${s.role}`;
		if (s.mode === 'dev') return 'Dev mode';
		return 'No sign-in';
	});
	const initials = $derived.by(() => {
		const src = s?.user?.name || s?.user?.email || '?';
		const parts = src.split(/[\s@.]+/).filter(Boolean);
		return ((parts[0]?.[0] ?? '?') + (parts[1]?.[0] ?? '')).toUpperCase();
	});

	function onWindowClick(e: MouseEvent) {
		if (menuOpen && menuEl && !menuEl.contains(e.target as Node)) menuOpen = false;
	}

	async function signOut() {
		menuOpen = false;
		await auth.signOut();
		goto(`${base}/login`);
	}

	onMount(() => {
		getReady()
			.then((r) => (ready = r))
			.catch(() => (ready = null));
	});
</script>

<svelte:window onclick={onWindowClick} />

<header class="sticky top-0 z-30 border-b bg-background/80 backdrop-blur">
	<div class="mx-auto flex h-14 w-full max-w-5xl items-center gap-3 px-4 sm:px-6">
		<a href="{base}/" class="flex items-center gap-2" aria-label="pushq home">
			<span class="flex h-6 w-6 items-center justify-center rounded-md bg-primary">
				<svg
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2.5"
					stroke-linecap="round"
					stroke-linejoin="round"
					class="h-3.5 w-3.5 text-primary-foreground"
					aria-hidden="true"
				>
					<path d="M12 19V5" />
					<path d="m5 12 7-7 7 7" />
				</svg>
			</span>
			<span class="text-base font-semibold tracking-tight">pushq</span>
		</a>

		<div class="ml-auto flex items-center gap-1.5">
			{#if ready}
				<span
					class="hidden rounded-full border px-2.5 py-0.5 font-mono text-xs text-muted-foreground sm:inline"
					title="Server dialect and role from /readyz"
				>
					{ready.dialect ?? '?'} · {ready.role ?? '?'}
				</span>
			{/if}
			{#if showAccount && s}
				<div class="relative" bind:this={menuEl}>
					<button
						type="button"
						class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-accent"
						aria-haspopup="menu"
						aria-expanded={menuOpen}
						onclick={() => (menuOpen = !menuOpen)}
					>
						{#if s.user}
							<span
								class="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-[10px] font-semibold text-primary-foreground"
								aria-hidden="true">{initials}</span
							>
						{/if}
						<span class="hidden max-w-40 truncate sm:inline">{label}</span>
						<svg class="h-3.5 w-3.5 text-muted-foreground" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
					</button>
					{#if menuOpen}
						<div
							role="menu"
							class="absolute right-0 top-full z-40 mt-1 w-60 rounded-lg border bg-background p-1 shadow-lg"
						>
							<div class="border-b px-3 py-2">
								{#if s.user}
									<p class="truncate text-sm font-medium">{s.user.name || s.user.email}</p>
									{#if s.user.name}<p class="truncate text-xs text-muted-foreground">{s.user.email}</p>{/if}
									<p class="mt-1 text-xs capitalize text-muted-foreground">{s.user.role}</p>
								{:else if s.kind === 'api_key'}
									<p class="text-sm font-medium">Signed in with an API key</p>
									<p class="text-xs text-muted-foreground">{s.key_name} · {s.role}</p>
								{:else}
									<p class="text-sm font-medium">{s.mode === 'dev' ? 'Dev mode' : 'No sign-in required'}</p>
									<p class="text-xs text-muted-foreground">Authentication is turned off.</p>
								{/if}
							</div>
							{#if s.user}
								<a role="menuitem" href="{base}/settings/account" class="block rounded-md px-3 py-1.5 text-sm hover:bg-accent" onclick={() => (menuOpen = false)}>Your account</a>
							{/if}
							{#if auth.can.manage && s.user}
								<a role="menuitem" href="{base}/settings/team" class="block rounded-md px-3 py-1.5 text-sm hover:bg-accent" onclick={() => (menuOpen = false)}>Team</a>
								<a role="menuitem" href="{base}/settings/api-keys" class="block rounded-md px-3 py-1.5 text-sm hover:bg-accent" onclick={() => (menuOpen = false)}>API keys</a>
							{/if}
							{#if s.kind === 'user' || s.kind === 'api_key'}
								<button role="menuitem" type="button" class="block w-full rounded-md px-3 py-1.5 text-left text-sm hover:bg-accent" onclick={signOut}>Sign out</button>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
			<ThemeToggle />
		</div>
	</div>
</header>
