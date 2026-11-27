<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { accountsApi, errorMessage, type Invite } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import AuthCard from '$lib/components/AuthCard.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import Field from '$lib/components/Field.svelte';

	// The token travels in the URL fragment, so it never reaches the server's
	// logs or other sites via Referer.
	let token = $state('');
	let invite = $state<Invite | null>(null);
	let loadError = $state<string | null>(null);
	let name = $state('');
	let password = $state('');
	let confirm = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);

	onMount(async () => {
		token = new URLSearchParams(window.location.hash.slice(1)).get('token') ?? '';
		if (!token) {
			loadError = 'This link is missing its token. Ask your admin for a new one.';
			return;
		}
		try {
			invite = await accountsApi.getInvite(token);
		} catch (e) {
			loadError = errorMessage(e);
		}
	});

	const isReset = $derived(invite?.kind === 'reset');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = null;
		if (password !== confirm) {
			error = 'Passwords do not match.';
			return;
		}
		busy = true;
		try {
			await accountsApi.acceptInvite(token, name, password);
			history.replaceState(null, '', window.location.pathname); // drop the used token
			await auth.refresh();
			goto(`${base}/`);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>{isReset ? 'Reset password' : 'Join team'} · pushq</title>
</svelte:head>

{#if loadError}
	<AuthCard title="Link not valid">
		<ErrorAlert message={loadError} />
		<a href="{base}/login" class="mt-4 block text-center text-sm text-muted-foreground hover:text-foreground">Go to sign in</a>
	</AuthCard>
{:else if !invite}
	<p class="py-16 text-center text-sm text-muted-foreground">Checking link…</p>
{:else}
	<AuthCard
		title={isReset ? 'Choose a new password' : 'Join pushq'}
		description={isReset
			? `For ${invite.email}. You'll be signed out on your other devices.`
			: `You've been invited as ${invite.role === 'admin' ? 'an' : 'a'} ${invite.role} (${invite.email}).`}
	>
		<form class="space-y-4" onsubmit={submit}>
			<ErrorAlert message={error} />
			{#if !isReset}
				<Field label="Your name" id="name">
					<Input id="name" autocomplete="name" bind:value={name} />
				</Field>
			{/if}
			<Field label={isReset ? 'New password' : 'Password'} id="password" hint="At least 8 characters.">
				<Input id="password" type="password" autocomplete="new-password" minlength={8} required bind:value={password} />
			</Field>
			<Field label="Confirm password" id="confirm">
				<Input id="confirm" type="password" autocomplete="new-password" required bind:value={confirm} />
			</Field>
			<Button type="submit" class="w-full" disabled={busy}>
				{busy ? 'Saving…' : isReset ? 'Set password and sign in' : 'Create account'}
			</Button>
		</form>
	</AuthCard>
{/if}
