<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { page } from '$app/state';
	import { prefersReducedMotion } from 'svelte/motion';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import ListIcon from '@lucide/svelte/icons/list';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import type { App } from '$lib/api';
	import { apps } from '$lib/apps.svelte';
	import { auth } from '$lib/auth.svelte';
	import { cn } from '$lib/utils';
	import AppAvatar from './AppAvatar.svelte';
	import AccountMenu from './AccountMenu.svelte';
	import AppDialog from './AppDialog.svelte';
	import RailTip from './RailTip.svelte';

	// The application switcher: one tile per application down the left edge,
	// with an edge mark that slides to the one on screen. Queues, tasks and
	// everything under them belong to the selected application. The foot of
	// the rail links to the queues, the API docs and settings.

	let createOpen = $state(false);
	const current = $derived(apps.current);
	const route = $derived(page.url.pathname.slice(base.length) || '/');

	function choose(app: App) {
		if (app.id === current?.id) return;
		apps.select(app.id);
		// A queue page (or folder) belongs to the old application; go back to its list.
		if (route.startsWith('/queues/') || page.url.searchParams.has('folder')) goto(`${base}/`);
	}

	async function created(app: App) {
		await apps.load();
		apps.select(app.id);
		goto(`${base}/`);
	}

	const links = $derived([
		{ href: `${base}/`, label: 'Queues', icon: ListIcon, active: route === '/' || route.startsWith('/queues') },
		{ href: '/docs', label: 'API docs', icon: BookOpenIcon, active: false, reload: true },
		{ href: `${base}/settings`, label: 'Settings', icon: SettingsIcon, active: route.startsWith('/settings') }
	]);

	// The edge mark: measured on mount, on every switch and when the rail resizes.
	let rail = $state<HTMLElement>();
	let mark = $state<{ y: number; instant: boolean; stretch: boolean }>();
	let stretchTimer: ReturnType<typeof setTimeout> | undefined;

	function placeMark(instant: boolean) {
		const el = rail?.querySelector<HTMLElement>('[data-app-selected]');
		if (!el) return (mark = undefined);
		const y = el.offsetTop + el.offsetHeight / 2 - 10;
		if (mark && mark.y === y) return;
		const moving = !instant && mark !== undefined && !prefersReducedMotion.current;
		mark = { y, instant: !moving, stretch: moving };
		clearTimeout(stretchTimer);
		if (moving) stretchTimer = setTimeout(() => mark && (mark.stretch = false), 180);
	}

	$effect(() => {
		void current?.id;
		void apps.list;
		requestAnimationFrame(() => placeMark(false));
	});

	$effect(() => {
		if (!rail) return;
		const ro = new ResizeObserver(() => placeMark(true));
		ro.observe(rail);
		return () => {
			ro.disconnect();
			clearTimeout(stretchTimer);
		};
	});
</script>

<nav
	bind:this={rail}
	aria-label="Applications"
	class="relative flex h-full w-14 shrink-0 flex-col items-center gap-2.5 border-r bg-background pb-3 pt-3"
>
	{#if apps.list?.length}
		<!-- level with the top bar's logo row -->
		<div class="flex h-8 items-center">
			<span class="font-mono text-[10px] uppercase tracking-wider text-subtle">Apps</span>
		</div>
	{/if}

	{#each apps.list ?? [] as app (app.id)}
		{@const selected = app.id === current?.id}
		<div class="group/tip relative" data-app-selected={selected || undefined}>
			<button
				type="button"
				class={cn(
					'relative block rounded-lg ring-offset-2 ring-offset-background transition-[transform,box-shadow,opacity] duration-200 hover:scale-105 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand',
					selected ? 'ring-1 ring-foreground/70' : 'opacity-75 hover:opacity-100'
				)}
				aria-current={selected ? 'true' : undefined}
				aria-label={app.name}
				onclick={() => choose(app)}
			>
				<AppAvatar id={app.id} name={app.name} icon={app.icon} />
			</button>
			<RailTip>
				{app.name}{#if app.default}<span class="ml-1.5 font-mono text-[10px] text-subtle">default</span>{/if}
			</RailTip>
		</div>
	{/each}

	{#if auth.can.manage && apps.list?.length}
		<div class="group/tip relative">
			<button
				type="button"
				class="grid size-8 place-items-center rounded-lg border border-dashed border-input text-muted-foreground transition-colors hover:border-foreground/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
				aria-label="New application"
				onclick={() => (createOpen = true)}
			>
				<PlusIcon class="size-4" strokeWidth={2} />
			</button>
			<RailTip>New application</RailTip>
		</div>
	{/if}

	<div class="flex-1"></div>

	<div class="flex flex-col items-center gap-1 border-t pt-3">
		{#each links as l (l.label)}
			<div class="group/tip relative">
				<a
					href={l.href}
					data-sveltekit-reload={l.reload ? '' : undefined}
					aria-label={l.label}
					aria-current={l.active ? 'page' : undefined}
					class={cn(
						'grid size-9 place-items-center rounded-lg transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand',
						l.active ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'
					)}
				>
					<l.icon class="size-[18px]" strokeWidth={1.7} />
				</a>
				<RailTip>{l.label}</RailTip>
			</div>
		{/each}
		<div class="mt-2">
			<AccountMenu />
		</div>
	</div>

	{#if mark}
		<span
			aria-hidden="true"
			class={[
				'pointer-events-none absolute left-0 top-0 z-[2] w-[3px] rounded-r-full bg-foreground',
				mark.instant ? 'transition-none' : '[transition:transform_.45s_cubic-bezier(.34,1.3,.4,1),height_.3s_ease]'
			]}
			style="transform: translateY({mark.y}px); height: {mark.stretch ? 30 : 20}px;"
		></span>
	{/if}
</nav>

<AppDialog bind:open={createOpen} onsaved={created} />
