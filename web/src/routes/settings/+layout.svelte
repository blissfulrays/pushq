<script lang="ts">
	import type { Snippet } from 'svelte';
	import { base } from '$app/paths';
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';
	import { cn } from '$lib/utils';

	let { children }: { children: Snippet } = $props();

	const tabs = $derived([
		{ href: `${base}/settings/account`, label: 'Your account', show: !!auth.user },
		{ href: `${base}/settings/team`, label: 'Team', show: auth.can.manage },
		{ href: `${base}/settings/api-keys`, label: 'API keys', show: auth.can.manage }
	].filter((t) => t.show));
</script>

<div class="space-y-6">
	<div>
		<h1 class="text-xl font-semibold tracking-tight">Settings</h1>
		<nav class="mt-4 flex gap-1 border-b" aria-label="Settings">
			{#each tabs as t (t.href)}
				<a
					href={t.href}
					class={cn(
						'-mb-px border-b-2 px-3 py-2 text-sm',
						page.url.pathname === t.href
							? 'border-primary font-medium text-foreground'
							: 'border-transparent text-muted-foreground hover:text-foreground'
					)}>{t.label}</a
				>
			{/each}
		</nav>
	</div>
	{@render children()}
</div>
