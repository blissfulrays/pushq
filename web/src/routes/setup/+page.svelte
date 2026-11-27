<script lang="ts">
	import { accountsApi, errorMessage } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import AuthCard from '$lib/components/AuthCard.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import Field from '$lib/components/Field.svelte';

	let name = $state('');
	let email = $state('');
	let password = $state('');
	let confirm = $state('');
	let apiKey = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);

	const needsKey = $derived(auth.session?.setup_requires_key ?? false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = null;
		if (password !== confirm) {
			error = 'Passwords do not match.';
			return;
		}
		busy = true;
		try {
			await accountsApi.setup({ name, email, password, api_key: needsKey ? apiKey : undefined });
			await auth.refresh(); // the layout then takes us to the dashboard
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>Set up · pushq</title>
</svelte:head>

<AuthCard
	title="Welcome to pushq"
	description="Create the admin account for this server. You can invite your team afterwards."
>
	<form class="space-y-4" onsubmit={submit}>
		<ErrorAlert message={error} />
		<Field label="Your name" id="name">
			<Input id="name" autocomplete="name" bind:value={name} />
		</Field>
		<Field label="Email" id="email">
			<Input id="email" type="email" autocomplete="username" required bind:value={email} />
		</Field>
		<Field label="Password" id="password" hint="At least 8 characters.">
			<Input id="password" type="password" autocomplete="new-password" minlength={8} required bind:value={password} />
		</Field>
		<Field label="Confirm password" id="confirm">
			<Input id="confirm" type="password" autocomplete="new-password" required bind:value={confirm} />
		</Field>
		{#if needsKey}
			<Field
				label="Admin API key"
				id="apikey"
				hint="This server is protected by API keys in its config, so creating the first account needs one of its admin keys."
			>
				<Input id="apikey" type="password" autocomplete="off" required bind:value={apiKey} />
			</Field>
		{/if}
		<div class="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
			Once this account exists, the API stops accepting anonymous requests. Apps will need an API
			key — create one under <span class="font-medium text-foreground">API keys</span> after setup.
		</div>
		<Button type="submit" class="w-full" disabled={busy}>
			{busy ? 'Creating account…' : 'Create admin account'}
		</Button>
	</form>
</AuthCard>
