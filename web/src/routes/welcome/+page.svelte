<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { errorMessage, teamsApi } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import AuthCard from '$lib/components/AuthCard.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import Field from '$lib/components/Field.svelte';

	let name = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);

	const teams = $derived(auth.session?.teams ?? []);
	const firstName = $derived((auth.user?.name || auth.user?.email || '').split(/[\s@]/)[0]);

	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		try {
			await teamsApi.create(name.trim());
			await auth.refresh();
			goto(`${base}/`);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function open(id: number) {
		error = null;
		try {
			await teamsApi.switchTo(id);
			await auth.refresh();
			goto(`${base}/`);
		} catch (err) {
			error = errorMessage(err);
		}
	}
</script>

<svelte:head>
	<title>Welcome · pushq</title>
</svelte:head>

<AuthCard
	title={firstName ? `Welcome, ${firstName}` : 'Welcome to pushq'}
	description="Queues, tasks and API keys belong to a team. Create one to get started; you can invite people next."
>
	<div class="space-y-5">
		<ErrorAlert message={error} />
		<form class="space-y-3" onsubmit={create}>
			<Field label="Team name" id="team" hint="Usually your company or project.">
				<Input id="team" required maxlength={100} placeholder="Acme" bind:value={name} />
			</Field>
			<Button type="submit" class="w-full" disabled={busy || !name.trim()}>
				{busy ? 'Creating…' : 'Create team'}
			</Button>
		</form>

		{#if teams.length > 0}
			<div class="space-y-2 border-t pt-4">
				<p class="text-xs text-muted-foreground">Or open a team you're already in:</p>
				{#each teams as t (t.id)}
					<button
						type="button"
						class="flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm hover:bg-accent"
						onclick={() => open(t.id)}
					>
						<span class="font-medium">{t.name}</span>
						<span class="text-xs capitalize text-muted-foreground">{t.role}</span>
					</button>
				{/each}
			</div>
		{:else}
			<p class="text-xs text-muted-foreground">
				Expecting an invite? Accept it from the email, then sign in again.
			</p>
		{/if}
	</div>
</AuthCard>
