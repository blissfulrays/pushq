<script lang="ts">
	import type { Component, Snippet } from 'svelte';
	import { base } from '$app/paths';
	import { page } from '$app/state';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import LayersIcon from '@lucide/svelte/icons/layers';
	import UserIcon from '@lucide/svelte/icons/user';
	import UsersIcon from '@lucide/svelte/icons/users';
	import { apps } from '$lib/apps.svelte';
	import { auth } from '$lib/auth.svelte';
	import { cn } from '$lib/utils';

	let { children }: { children: Snippet } = $props();

	type Item = {
		href: string;
		label: string;
		/** Label on the phone tab strip. */
		short: string;
		icon: Component<{ class?: string; strokeWidth?: number }>;
		count?: number;
		show: boolean;
	};

	const groups = $derived(
		[
			{
				label: 'Account',
				items: [
					{ href: `${base}/settings/account`, label: 'Your account', short: 'Account', icon: UserIcon, show: !!auth.user }
				]
			},
			{
				label: 'Workspace',
				items: [
					{
						href: `${base}/settings/apps`,
						label: 'Applications',
						short: 'Apps',
						icon: LayersIcon,
						count: apps.list?.length,
						show: auth.can.read && (apps.list?.length ?? 0) > 0
					},
					{
						href: `${base}/settings/team`,
						label: auth.saas ? 'Team' : 'Team members',
						short: 'Team',
						icon: UsersIcon,
						show: auth.can.manage
					}
				]
			},
			{
				label: 'Developers',
				items: [
					{ href: `${base}/settings/api-keys`, label: 'API keys', short: 'API keys', icon: KeyRoundIcon, show: auth.can.manage }
				]
			}
		]
			.map((g) => ({ ...g, items: (g.items as Item[]).filter((i) => i.show) }))
			.filter((g) => g.items.length > 0)
	);
	const items = $derived(groups.flatMap((g) => g.items));
	const selected = $derived(page.url.pathname.replace(/\/$/, ''));

	// One selection pill for every group: it glides to the selected item.
	let nav = $state<HTMLElement>();
	let pill = $state<{ y: number; slide: boolean }>();
	$effect(() => {
		void selected;
		void items.length;
		requestAnimationFrame(() => {
			const el = nav?.querySelector<HTMLElement>('[aria-current="page"]');
			if (!el) return (pill = undefined);
			pill = { y: el.offsetTop, slide: pill !== undefined };
		});
	});
</script>

<div class="grid gap-6 md:grid-cols-[220px_minmax(0,1fr)] md:gap-10">
	<aside aria-label="Settings">
		<p class="px-2.5 font-mono text-[13px] text-subtle">console</p>
		<h1 class="mt-1 px-2.5 text-[28px] font-semibold leading-tight tracking-[-0.05em]">Settings</h1>

		<!-- Desktop: grouped list with a sliding pill. -->
		<div bind:this={nav} class="relative hidden md:block">
			{#if pill}
				<span
					class={[
						'pointer-events-none absolute inset-x-0 top-0 h-10 rounded-[10px] bg-accent',
						pill.slide && 'transition-transform duration-300 ease-out motion-reduce:transition-none'
					]}
					style="transform: translateY({pill.y}px)"
					aria-hidden="true"
				></span>
			{/if}
			{#each groups as group (group.label)}
				<div class="mt-5">
					<span class="block px-2.5 pb-1.5 font-mono text-[11px] uppercase tracking-wider text-subtle">{group.label}</span>
					<div class="flex flex-col gap-0.5">
						{#each group.items as item (item.href)}
							{@const current = item.href === selected}
							<a
								href={item.href}
								aria-current={current ? 'page' : undefined}
								class={cn(
									'group/nav relative z-[1] flex h-10 items-center gap-2.5 rounded-[10px] px-2.5 text-sm font-medium transition-colors',
									current ? 'text-foreground' : 'text-foreground/75 hover:bg-accent/60 hover:text-foreground'
								)}
							>
								<item.icon
									class={cn(
										'size-[18px] flex-none transition-[color,transform] duration-300',
										current ? 'scale-105 text-foreground' : 'text-muted-foreground group-hover/nav:text-foreground'
									)}
									strokeWidth={1.7}
								/>
								{item.label}
								{#if item.count !== undefined}
									<span class="ml-auto rounded-md border bg-background px-1.5 text-[11px] font-semibold tabular-nums text-muted-foreground">
										{item.count}
									</span>
								{/if}
							</a>
						{/each}
					</div>
				</div>
			{/each}
		</div>

		<!-- Phone and tablet: a strip of pills. -->
		<nav class="mt-3 flex gap-1.5 overflow-x-auto pb-1 md:hidden" aria-label="Settings sections">
			{#each items as item (item.href)}
				<a
					href={item.href}
					aria-current={item.href === selected ? 'page' : undefined}
					class={cn(
						'h-8 whitespace-nowrap rounded-full px-3.5 text-sm font-medium leading-8 transition-colors',
						item.href === selected ? 'bg-foreground text-background' : 'text-muted-foreground hover:text-foreground'
					)}>{item.short}</a
				>
			{/each}
		</nav>
	</aside>

	<section class="pq-rise min-w-0">
		{@render children()}
	</section>
</div>
