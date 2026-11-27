<script lang="ts">
	import { accountsApi, errorMessage } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import AuthCard from '$lib/components/AuthCard.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import Field from '$lib/components/Field.svelte';

	let email = $state('');
	let password = $state('');
	let key = $state('');
	let useKey = $state(false);
	let busy = $state(false);
	let error = $state<string | null>(null);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		try {
			if (useKey) {
				auth.setKey(key);
				const s = await auth.refresh();
				if (!s.authenticated) {
					auth.setKey('');
					error = 'That API key was not accepted.';
				}
			} else {
				await accountsApi.login(email, password);
				password = '';
				await auth.refresh(); // the layout then takes us onward
			}
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>Sign in · pushq</title>
</svelte:head>

<AuthCard
	title="Sign in"
	description={useKey ? 'Use an API key for this browser tab only.' : 'Sign in to manage your queues.'}
>
	<form class="space-y-4" onsubmit={submit}>
		<ErrorAlert message={error} />
		{#if useKey}
			<Field label="API key" id="key">
				<Input id="key" type="password" autocomplete="off" required bind:value={key} />
			</Field>
		{:else}
			<Field label="Email" id="email">
				<Input id="email" type="email" autocomplete="username" required bind:value={email} />
			</Field>
			<Field label="Password" id="password">
				<Input id="password" type="password" autocomplete="current-password" required bind:value={password} />
			</Field>
		{/if}
		<Button type="submit" class="w-full" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</Button>
		<button
			type="button"
			class="w-full text-center text-xs text-muted-foreground hover:text-foreground"
			onclick={() => {
				useKey = !useKey;
				error = null;
			}}
		>
			{useKey ? 'Sign in with email and password' : 'Use an API key instead'}
		</button>
		{#if !useKey}
			<p class="text-center text-xs text-muted-foreground">
				Forgot your password? Ask a team admin for a reset link.
			</p>
		{/if}
	</form>
</AuthCard>
