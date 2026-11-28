<script lang="ts">
	import ArrowUpRightIcon from '@lucide/svelte/icons/arrow-up-right';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import { accountsApi, errorMessage, type ApiKey, type KeyRole } from '$lib/api';
	import { apps } from '$lib/apps.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import Dialog from '$lib/components/ui/dialog/dialog.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import CopyField from '$lib/components/CopyField.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';
	import EmptyState from '$lib/components/settings/EmptyState.svelte';
	import PanelHeader from '$lib/components/settings/PanelHeader.svelte';
	import SettingsRow from '$lib/components/settings/SettingsRow.svelte';

	let keys = $state<ApiKey[] | null>(null);
	let configKeys = $state(0);
	let error = $state<string | null>(null);

	// Generate dialog
	let createOpen = $state(false);
	let name = $state('');
	let role = $state<KeyRole>('writer');
	/** "" = every application; otherwise an application id. */
	let appId = $state('');
	let nameInput = $state<HTMLInputElement | null>(null);
	let creating = $state(false);
	let createErr = $state<string | null>(null);

	let secret = $state('');
	let secretOpen = $state(false);

	let revokeTarget = $state<ApiKey | null>(null);
	let revokeOpen = $state(false);
	let busy = $state(false);

	const selectClass = 'h-9 w-full rounded-md border border-input bg-background px-2 text-sm';

	async function load() {
		try {
			const res = await accountsApi.listKeys();
			keys = res.keys;
			configKeys = res.config_keys;
			error = null;
		} catch (e) {
			error = errorMessage(e);
		}
	}

	$effect(() => {
		load();
	});

	function openCreate() {
		name = '';
		role = 'writer';
		// Start from the application on screen: the common case is a key for it.
		appId = apps.current && (apps.list?.length ?? 0) > 1 ? String(apps.current.id) : '';
		createErr = null;
		createOpen = true;
		requestAnimationFrame(() => nameInput?.focus());
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		createErr = null;
		try {
			const res = await accountsApi.createKey(name, role, appId ? Number(appId) : undefined);
			createOpen = false;
			secret = res.secret;
			secretOpen = true;
			await load();
		} catch (err) {
			createErr = errorMessage(err);
		} finally {
			creating = false;
		}
	}

	async function revoke() {
		if (!revokeTarget) return;
		busy = true;
		try {
			await accountsApi.deleteKey(revokeTarget.id);
			await load();
		} catch (e) {
			error = errorMessage(e);
		} finally {
			busy = false;
			revokeOpen = false;
		}
	}
</script>

<svelte:head>
	<title>API keys · pushq</title>
</svelte:head>

<PanelHeader title="API keys">
	{#snippet descriptionSnippet()}
		Apps and scripts send a key as
		<code class="rounded bg-muted px-1 font-mono text-xs">Authorization: Bearer …</code>. A key can reach every
		application or just one.
		<a
			href="/docs"
			data-sveltekit-reload
			class="group/docs inline-flex items-center gap-0.5 font-medium text-foreground underline-offset-4 hover:underline"
			>SDK API docs<ArrowUpRightIcon
				class="size-3.5 transition-transform group-hover/docs:-translate-y-0.5 group-hover/docs:translate-x-0.5"
			/></a
		>
	{/snippet}
	{#snippet action()}
		<Button onclick={openCreate}><PlusIcon />Generate key</Button>
	{/snippet}
</PanelHeader>

<ErrorAlert message={error} class="mt-4" />

{#if keys === null}
	<p class="py-12 text-center text-sm text-muted-foreground">Loading…</p>
{:else if keys.length === 0}
	<EmptyState icon={KeyRoundIcon} title="No API keys yet">Generate one to call the pushq API from your app.</EmptyState>
{:else}
	<div>
		{#each keys as k (k.id)}
			<SettingsRow icon={KeyRoundIcon}>
				{#snippet body()}
					<b class="flex min-w-0 items-center gap-1.5 text-[15px] font-medium">
						<span class="truncate">{k.name}</span>
						<em class="rounded-md bg-muted px-1.5 text-xs font-semibold capitalize not-italic text-muted-foreground"
							>{k.role}</em
						>
					</b>
					<span class="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-[13.5px] text-muted-foreground">
						<span class="font-mono text-xs">{k.prefix}••••</span>
						{#if k.app_id}
							<span>Only <span class="font-medium text-foreground">{k.app_name}</span></span>
						{:else}
							<span>All applications</span>
						{/if}
						<span>Created <TimeStamp value={k.created_at} />{#if k.created_by}&nbsp;by {k.created_by}{/if}</span>
						<span>
							{#if k.last_used_at}Last used <TimeStamp value={k.last_used_at} />{:else}Never used{/if}
						</span>
					</span>
				{/snippet}
				{#snippet ctl()}
					<Button
						variant="ghost"
						size="sm"
						class="text-destructive hover:text-destructive"
						onclick={() => {
							revokeTarget = k;
							revokeOpen = true;
						}}>Revoke</Button
					>
				{/snippet}
			</SettingsRow>
		{/each}
	</div>
{/if}

{#if configKeys > 0}
	<p class="mt-4 text-xs text-muted-foreground">
		{configKeys} more key{configKeys === 1 ? ' is' : 's are'} defined in the server config file (<code
			class="font-mono">api_keys</code
		>); change those there.
	</p>
{/if}

<Dialog bind:open={createOpen} title="Generate an API key" description="The key is shown once, right after it's made.">
	<form id="key-form" class="space-y-4" onsubmit={create}>
		<label class="block space-y-1.5">
			<span class="text-sm font-medium">Name</span>
			<Input
				bind:ref={nameInput}
				placeholder="What uses it, e.g. billing-service"
				required
				maxlength={100}
				bind:value={name}
			/>
		</label>
		<label class="block space-y-1.5">
			<span class="text-sm font-medium">Application</span>
			<select bind:value={appId} class={selectClass}>
				<option value="">All applications</option>
				{#each apps.list ?? [] as a (a.id)}
					<option value={String(a.id)}>Only {a.name}</option>
				{/each}
			</select>
			<span class="block text-xs text-muted-foreground">
				{#if appId}
					The key can reach this application's queues and nothing else.
				{:else}
					The key uses the Default application unless a request sends
					<code class="font-mono">X-PushQ-App</code>.
				{/if}
			</span>
		</label>
		<label class="block space-y-1.5">
			<span class="text-sm font-medium">Role</span>
			<select bind:value={role} class={selectClass}>
				<option value="writer">Writer</option>
				<option value="admin">Admin</option>
			</select>
			<span class="block text-xs text-muted-foreground">
				{#if role === 'writer'}
					Creates and manages queues and tasks it knows by name; can't list or delete queues.
				{:else if appId}
					Full access to the application's queues, including listing and deleting them.
				{:else}
					Full access, including listing and deleting queues, team and keys.
				{/if}
			</span>
		</label>
		<ErrorAlert message={createErr} />
	</form>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button type="submit" form="key-form" disabled={creating || !name.trim()}>
			{creating ? 'Generating…' : 'Generate key'}
		</Button>
	{/snippet}
</Dialog>

<Dialog
	bind:open={secretOpen}
	title="Copy your new API key"
	description="This is the only time it will be shown. Store it somewhere safe, like your app's secret manager."
>
	<CopyField value={secret} />
	{#snippet footer()}
		<Button
			onclick={() => {
				secretOpen = false;
				secret = '';
			}}>I've saved it</Button
		>
	{/snippet}
</Dialog>

<ConfirmDialog
	bind:open={revokeOpen}
	title={`Revoke "${revokeTarget?.name ?? ''}"?`}
	description="Anything using this key will start getting 401 errors right away. This can't be undone."
	confirmLabel="Revoke key"
	{busy}
	onconfirm={revoke}
/>
