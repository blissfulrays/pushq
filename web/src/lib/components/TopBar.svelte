<script lang="ts">
	import { onMount } from 'svelte';
	import { base } from '$app/paths';
	import { getReady, type Ready } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import ThemeToggle from './ThemeToggle.svelte';

	let ready = $state<Ready | null>(null);

	onMount(() => {
		getReady()
			.then((r) => (ready = r))
			.catch(() => (ready = null));
	});
</script>

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
			<Button variant="ghost" size="sm" onclick={() => auth.requireKey()}>
				<svg
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<path
						d="M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z"
					/>
					<circle cx="16.5" cy="7.5" r=".5" fill="currentColor" />
				</svg>
				API key
			</Button>
			<ThemeToggle />
		</div>
	</div>
</header>
