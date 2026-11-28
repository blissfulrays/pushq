<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import LayersIcon from '@lucide/svelte/icons/layers';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import { appsApi, errorMessage, type App } from '$lib/api';
	import { apps } from '$lib/apps.svelte';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import AppAvatar from '$lib/components/apps/AppAvatar.svelte';
	import AppDialog from '$lib/components/apps/AppDialog.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import EmptyState from '$lib/components/settings/EmptyState.svelte';
	import PanelHeader from '$lib/components/settings/PanelHeader.svelte';
	import SettingsRow from '$lib/components/settings/SettingsRow.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';

	let dialogOpen = $state(false);
	let editing = $state<App | null>(null);
	let deleteTarget = $state<App | null>(null);
	let deleteOpen = $state(false);
	let busy = $state(false);
	let error = $state<string | null>(null);

	const list = $derived(apps.list ?? []);
	const canManage = $derived(auth.can.manage);

	function openApp(app: App) {
		apps.select(app.id);
		goto(`${base}/`);
	}

	function newApp() {
		editing = null;
		dialogOpen = true;
	}

	function edit(app: App) {
		editing = app;
		dialogOpen = true;
	}

	async function saved(app: App) {
		await apps.load();
		if (!editing) apps.select(app.id);
	}

	async function remove() {
		if (!deleteTarget) return;
		busy = true;
		error = null;
		try {
			await appsApi.remove(deleteTarget.id);
			await apps.load();
		} catch (e) {
			error = errorMessage(e);
		} finally {
			busy = false;
			deleteOpen = false;
		}
	}
</script>

<svelte:head>
	<title>Applications · pushq</title>
</svelte:head>

<PanelHeader title="Applications">
	{#snippet descriptionSnippet()}
		Every queue belongs to an application. Switch between them from the rail on the left; API calls pick one
		with the <code class="rounded bg-muted px-1 font-mono text-xs">X-PushQ-App</code> header, or use a key made for it.
	{/snippet}
	{#snippet action()}
		{#if canManage}
			<Button onclick={newApp}><PlusIcon />New application</Button>
		{/if}
	{/snippet}
</PanelHeader>

<ErrorAlert message={error ?? apps.error} class="mt-4" />

{#if apps.list === null}
	<p class="py-12 text-center text-sm text-muted-foreground">Loading…</p>
{:else if list.length === 0}
	<EmptyState icon={LayersIcon} title="No applications">Create one to start adding queues.</EmptyState>
{:else}
	<div>
		{#each list as app (app.id)}
			{@const current = app.id === apps.current?.id}
			<SettingsRow>
				{#snippet lead()}
					<AppAvatar id={app.id} name={app.name} icon={app.icon} class="size-10 rounded-xl text-xs" />
				{/snippet}
				{#snippet body()}
					<b class="flex min-w-0 items-center gap-1.5 text-[15px] font-medium">
						<span class="truncate">{app.name}</span>
						{#if app.default}
							<em class="rounded-md bg-muted px-1.5 text-xs font-semibold not-italic text-muted-foreground">Default</em>
						{/if}
					</b>
					<span class="mt-0.5 flex flex-wrap items-center gap-x-2.5 text-[13.5px] text-muted-foreground">
						<span class="inline-flex items-center gap-1.5">
							<i class={['size-1.5 rounded-full', current ? 'bg-success' : 'bg-muted-foreground/40']}></i>
							{current ? 'Showing now' : 'Not selected'}
						</span>
						<span class="font-mono text-xs">X-PushQ-App: {app.id}</span>
						<span>Created <TimeStamp value={app.created_at} /></span>
					</span>
				{/snippet}
				{#snippet ctl()}
					{#if !current}
						<Button variant="secondary" size="sm" onclick={() => openApp(app)}>Open</Button>
					{/if}
					{#if canManage}
						<Button variant="ghost" size="sm" onclick={() => edit(app)}>Edit</Button>
						{#if !app.default}
							<Button
								variant="ghost"
								size="sm"
								class="text-destructive hover:text-destructive"
								onclick={() => {
									deleteTarget = app;
									deleteOpen = true;
								}}>Delete</Button
							>
						{/if}
					{/if}
				{/snippet}
			</SettingsRow>
		{/each}
	</div>
{/if}

<AppDialog bind:open={dialogOpen} app={editing} onsaved={saved} />

<ConfirmDialog
	bind:open={deleteOpen}
	title={`Delete "${deleteTarget?.name ?? ''}"?`}
	description="Only an application with no queues can be deleted. API keys made for it are revoked, so anything using them gets 401 errors right away."
	confirmLabel="Delete application"
	{busy}
	onconfirm={remove}
/>
