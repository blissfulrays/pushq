<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import CheckIcon from '@lucide/svelte/icons/check';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import { teamsApi } from '$lib/api';
	import { appContext } from '$lib/app-context.svelte';
	import { auth } from '$lib/auth.svelte';
	import RailTip from './RailTip.svelte';

	// Who is signed in, their teams (SaaS) and sign-out, at the foot of the rail.

	let open = $state(false);
	let el = $state<HTMLElement | null>(null);

	const s = $derived(auth.session);
	const label = $derived.by(() => {
		if (!s) return '';
		if (s.user) return s.user.name || s.user.email;
		if (s.kind === 'api_key') return `API key · ${s.role}`;
		if (s.mode === 'dev') return 'Dev mode';
		return 'No sign-in';
	});
	const initials = $derived.by(() => {
		const src = s?.user?.name || s?.user?.email || (s?.mode === 'dev' ? 'dev' : '?');
		const parts = src.split(/[\s@.]+/).filter(Boolean);
		return ((parts[0]?.[0] ?? '?') + (parts[1]?.[0] ?? '')).toUpperCase();
	});

	function onWindowClick(e: MouseEvent) {
		if (open && el && !el.contains(e.target as Node)) open = false;
	}

	async function switchTeam(id: number) {
		open = false;
		if (id === s?.team?.id) return;
		await teamsApi.switchTo(id);
		appContext.set(null); // the other team has its own applications
		await auth.refresh();
		goto(`${base}/`);
	}

	async function signOut() {
		open = false;
		await auth.signOut();
		goto(`${base}/login`);
	}

	const item = 'flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-sm hover:bg-muted';
</script>

<svelte:window onclick={onWindowClick} onkeydown={(e) => e.key === 'Escape' && (open = false)} />

{#if s}
	<div class="group/tip relative" bind:this={el}>
		<button
			type="button"
			class="grid size-8 place-items-center rounded-full bg-primary font-mono text-[10px] font-semibold text-primary-foreground ring-offset-2 ring-offset-background transition-shadow hover:ring-1 hover:ring-foreground/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
			aria-haspopup="menu"
			aria-expanded={open}
			aria-label="Account: {label}"
			onclick={() => (open = !open)}
		>
			{#if s.kind === 'api_key'}<KeyRoundIcon class="size-3.5" />{:else}{initials}{/if}
		</button>
		{#if !open}<RailTip>{label}</RailTip>{/if}

		{#if open}
			<div
				role="menu"
				class="absolute bottom-0 left-[calc(100%+12px)] z-50 w-64 rounded-xl border bg-popover p-1 text-popover-foreground shadow-2xl"
			>
				<div class="border-b px-2.5 pb-2 pt-1.5">
					{#if s.user}
						<p class="truncate text-sm font-medium">{s.user.name || s.user.email}</p>
						{#if s.user.name}<p class="truncate text-xs text-muted-foreground">{s.user.email}</p>{/if}
						<p class="mt-1 font-mono text-[11px] text-subtle">{s.user.role}{s.team ? ` · ${s.team.name}` : ''}</p>
					{:else if s.kind === 'api_key'}
						<p class="text-sm font-medium">Signed in with an API key</p>
						<p class="font-mono text-[11px] text-subtle">{s.key_name} · {s.role}</p>
					{:else}
						<p class="text-sm font-medium">{s.mode === 'dev' ? 'Dev mode' : 'No sign-in required'}</p>
						<p class="text-xs text-muted-foreground">Authentication is turned off.</p>
					{/if}
				</div>
				{#if s.mode === 'saas' && s.user}
					<div class="border-b py-1">
						<p class="px-2.5 pb-1 pt-1.5 font-mono text-[10px] uppercase tracking-wider text-subtle">Teams</p>
						{#each s.teams ?? [] as t (t.id)}
							<button
								role="menuitemradio"
								aria-checked={t.id === s.team?.id}
								type="button"
								class="{item} justify-between"
								onclick={() => switchTeam(t.id)}
							>
								<span class="truncate">{t.name}</span>
								{#if t.id === s.team?.id}<CheckIcon class="size-3.5 text-muted-foreground" />{/if}
							</button>
						{/each}
						<a role="menuitem" href="{base}/welcome" class="{item} text-muted-foreground hover:text-foreground" onclick={() => (open = false)}>
							<PlusIcon class="size-3.5" />Create team
						</a>
					</div>
				{/if}
				{#if s.kind === 'user' || s.kind === 'api_key'}
					<div class="pt-1">
						<button role="menuitem" type="button" class={item} onclick={signOut}>
							<LogOutIcon class="size-3.5 text-muted-foreground" />Sign out
						</button>
					</div>
				{/if}
			</div>
		{/if}
	</div>
{/if}
