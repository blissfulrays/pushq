<script lang="ts">
	import { accountsApi, errorMessage, type ApiKey, type KeyRole } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import * as Table from '$lib/components/ui/table';
	import Dialog from '$lib/components/ui/dialog/dialog.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import CopyField from '$lib/components/CopyField.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';

	let keys = $state<ApiKey[] | null>(null);
	let configKeys = $state(0);
	let error = $state<string | null>(null);

	let name = $state('');
	let role = $state<KeyRole>('writer');
	let creating = $state(false);
	let createErr = $state<string | null>(null);

	let secret = $state('');
	let secretOpen = $state(false);

	let revokeTarget = $state<ApiKey | null>(null);
	let revokeOpen = $state(false);
	let busy = $state(false);

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

	async function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		createErr = null;
		try {
			const res = await accountsApi.createKey(name, role);
			secret = res.secret;
			secretOpen = true;
			name = '';
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
			revokeOpen = false;
			await load();
		} catch (e) {
			error = errorMessage(e);
			revokeOpen = false;
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>API keys · pushq</title>
</svelte:head>

<div class="space-y-6">
	<ErrorAlert message={error} />

	<Card.Root>
		<Card.Header>
			<Card.Title>Create an API key</Card.Title>
			<Card.Description>
				Apps and scripts send it as <code class="rounded bg-muted px-1 font-mono text-xs">Authorization: Bearer …</code>.
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form class="flex flex-col gap-2 sm:flex-row" onsubmit={create}>
				<Input placeholder="What uses it, e.g. billing-service" required maxlength={100} class="sm:flex-1" bind:value={name} />
				<select bind:value={role} class="h-9 rounded-md border border-input bg-background px-2 text-sm" aria-label="Key role">
					<option value="writer">Writer</option>
					<option value="admin">Admin</option>
				</select>
				<Button type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create key'}</Button>
			</form>
			<ErrorAlert message={createErr} class="mt-3" />
			<ul class="mt-3 space-y-0.5 text-xs text-muted-foreground">
				<li><span class="font-medium text-foreground">Writer</span> — create and manage queues and tasks it knows by name; can't list or delete queues</li>
				<li><span class="font-medium text-foreground">Admin</span> — full access, including listing and deleting queues, team and keys</li>
			</ul>
		</Card.Content>
	</Card.Root>

	<div class="rounded-xl border">
		<Table.Root>
			<Table.Header>
				<Table.Row>
					<Table.Head>Name</Table.Head>
					<Table.Head>Key</Table.Head>
					<Table.Head>Role</Table.Head>
					<Table.Head class="hidden md:table-cell">Created</Table.Head>
					<Table.Head class="hidden md:table-cell">Last used</Table.Head>
					<Table.Head></Table.Head>
				</Table.Row>
			</Table.Header>
			<Table.Body>
				{#if keys === null}
					<Table.Row><Table.Cell colspan={6} class="h-16 text-center text-muted-foreground">Loading…</Table.Cell></Table.Row>
				{:else if keys.length === 0}
					<Table.Row>
						<Table.Cell colspan={6} class="h-16 text-center text-muted-foreground">No keys created in the dashboard yet.</Table.Cell>
					</Table.Row>
				{:else}
					{#each keys as k (k.id)}
						<Table.Row>
							<Table.Cell class="font-medium">{k.name}</Table.Cell>
							<Table.Cell class="font-mono text-xs text-muted-foreground">{k.prefix}…</Table.Cell>
							<Table.Cell class="capitalize">{k.role}</Table.Cell>
							<Table.Cell class="hidden text-muted-foreground md:table-cell">
								<TimeStamp value={k.created_at} />{#if k.created_by}<span class="block text-xs">by {k.created_by}</span>{/if}
							</Table.Cell>
							<Table.Cell class="hidden text-muted-foreground md:table-cell">
								{#if k.last_used_at}<TimeStamp value={k.last_used_at} />{:else}never{/if}
							</Table.Cell>
							<Table.Cell class="text-right">
								<Button
									variant="ghost"
									size="sm"
									class="text-destructive hover:text-destructive"
									onclick={() => {
										revokeTarget = k;
										revokeOpen = true;
									}}>Revoke</Button
								>
							</Table.Cell>
						</Table.Row>
					{/each}
				{/if}
			</Table.Body>
		</Table.Root>
	</div>

	{#if configKeys > 0}
		<p class="text-xs text-muted-foreground">
			{configKeys} more key{configKeys === 1 ? ' is' : 's are'} defined in the server config file (<code class="font-mono">api_keys</code>); change those there.
		</p>
	{/if}
</div>

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
