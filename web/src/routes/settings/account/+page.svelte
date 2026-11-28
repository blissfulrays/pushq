<script lang="ts">
	import { accountsApi, errorMessage } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import Field from '$lib/components/Field.svelte';
	import PanelHeader from '$lib/components/settings/PanelHeader.svelte';

	let name = $state(auth.user?.name ?? '');
	let nameMsg = $state<string | null>(null);
	let nameErr = $state<string | null>(null);

	let current = $state('');
	let next = $state('');
	let confirm = $state('');
	let pwMsg = $state<string | null>(null);
	let pwErr = $state<string | null>(null);
	let busy = $state(false);

	async function saveName(e: SubmitEvent) {
		e.preventDefault();
		nameErr = nameMsg = null;
		try {
			await accountsApi.updateMe(name);
			await auth.refresh();
			nameMsg = 'Saved.';
		} catch (err) {
			nameErr = errorMessage(err);
		}
	}

	async function changePassword(e: SubmitEvent) {
		e.preventDefault();
		pwErr = pwMsg = null;
		if (next !== confirm) {
			pwErr = 'New passwords do not match.';
			return;
		}
		busy = true;
		try {
			await accountsApi.changePassword(current, next);
			current = next = confirm = '';
			pwMsg = 'Password changed. Your other devices have been signed out.';
		} catch (err) {
			pwErr = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>Your account · pushq</title>
</svelte:head>

<PanelHeader title="Your account" description="Your name, how you sign in, and your password." />

{#if !auth.user}
	<p class="mt-6 text-sm text-muted-foreground">You're not signed in with a dashboard account.</p>
{:else}
	<div class="mt-6 grid gap-6 lg:grid-cols-2">
		<Card.Root>
			<Card.Header>
				<Card.Title>Profile</Card.Title>
				<Card.Description>{auth.user.email} · <span class="capitalize">{auth.user.role}</span></Card.Description>
			</Card.Header>
			<Card.Content>
				<form class="space-y-4" onsubmit={saveName}>
					<ErrorAlert message={nameErr} />
					<Field label="Name" id="name">
						<Input id="name" autocomplete="name" bind:value={name} />
					</Field>
					<div class="flex items-center gap-3">
						<Button type="submit">Save</Button>
						{#if nameMsg}<span class="text-sm text-muted-foreground">{nameMsg}</span>{/if}
					</div>
				</form>
			</Card.Content>
		</Card.Root>

		{#if auth.saas}
			<Card.Root>
				<Card.Header>
					<Card.Title>Sign-in</Card.Title>
					<Card.Description>
						You sign in with a code emailed to {auth.user.email}, or with GitHub. There's no
						password to manage.
					</Card.Description>
				</Card.Header>
			</Card.Root>
		{:else}
		<Card.Root>
			<Card.Header>
				<Card.Title>Password</Card.Title>
				<Card.Description>Changing it signs you out on other devices.</Card.Description>
			</Card.Header>
			<Card.Content>
				<form class="space-y-4" onsubmit={changePassword}>
					<ErrorAlert message={pwErr} />
					<Field label="Current password" id="current">
						<Input id="current" type="password" autocomplete="current-password" required bind:value={current} />
					</Field>
					<Field label="New password" id="new" hint="At least 8 characters.">
						<Input id="new" type="password" autocomplete="new-password" minlength={8} required bind:value={next} />
					</Field>
					<Field label="Confirm new password" id="confirm">
						<Input id="confirm" type="password" autocomplete="new-password" required bind:value={confirm} />
					</Field>
					<div class="flex items-center gap-3">
						<Button type="submit" disabled={busy}>{busy ? 'Saving…' : 'Change password'}</Button>
						{#if pwMsg}<span class="text-sm text-muted-foreground">{pwMsg}</span>{/if}
					</div>
				</form>
			</Card.Content>
		</Card.Root>
		{/if}
	</div>
{/if}
