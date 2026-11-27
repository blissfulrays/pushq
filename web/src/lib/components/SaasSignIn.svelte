<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, emailCodeApi, emailVerifyApi, errorMessage, githubLoginUrl } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import AuthCard from '$lib/components/AuthCard.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import Field from '$lib/components/Field.svelte';

	// Sign in (or sign up: a new email gets an account) with GitHub or a
	// 6-digit code sent by email. Everything happens on this page except
	// GitHub's own consent screen.
	let { next = '', initialError = null }: { next?: string; initialError?: string | null } = $props();

	// email → code (email-code sign-in); verify = WorkOS paused the sign-in
	// until the person enters the verification code it emailed.
	let step = $state<'email' | 'code' | 'verify'>('email');
	let email = $state('');
	let code = $state('');
	let busy = $state(false);
	let error = $state<string | null>(untrack(() => initialError));
	let notice = $state<string | null>(null);
	let codeInput = $state<HTMLInputElement | null>(null);

	async function sendCode(e?: SubmitEvent) {
		e?.preventDefault();
		busy = true;
		error = notice = null;
		try {
			await emailCodeApi.send(email.trim());
			step = 'code';
			code = '';
			notice = `We sent a 6-digit code to ${email.trim()}. It expires in 10 minutes.`;
			queueMicrotask(() => codeInput?.focus());
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	function startVerify(addr: string | undefined) {
		step = 'verify';
		code = '';
		error = null;
		notice = `To finish signing in, enter the verification code we just sent to ${addr || 'your email'}.`;
		queueMicrotask(() => codeInput?.focus());
	}

	// Coming back from GitHub with a sign-in waiting for verification.
	onMount(async () => {
		if (page.url.searchParams.get('verify') !== '1') return;
		try {
			const p = await emailVerifyApi.pending();
			if (p.pending) startVerify(p.email);
			else error = 'That verification has expired. Please sign in again.';
		} catch (err) {
			error = errorMessage(err);
		}
	});

	async function verify(e?: SubmitEvent) {
		e?.preventDefault();
		busy = true;
		error = null;
		try {
			if (step === 'verify') {
				const res = await emailVerifyApi.submit(code.trim());
				await auth.refresh();
				goto(res.next || '/');
				return;
			}
			await emailCodeApi.verify(email.trim(), code.trim());
			await auth.refresh(); // the layout then takes us onward
		} catch (err) {
			if (err instanceof ApiError && err.code === 'email_verification_required') {
				startVerify(email.trim());
			} else {
				error = errorMessage(err);
			}
			busy = false;
		}
	}

	// Submit as soon as all six digits are in (including paste and autofill).
	function onCodeInput() {
		code = code.replace(/\D/g, '').slice(0, 6);
		if (code.length === 6 && !busy) verify();
	}

	function useDifferentEmail() {
		step = 'email';
		code = '';
		error = notice = null;
	}
</script>

<AuthCard
	title={step === 'email' ? 'Sign in to pushq' : step === 'verify' ? 'Verify your email' : 'Check your email'}
	description={step === 'email'
		? 'New here? Signing in creates your account.'
		: step === 'verify'
			? 'One-time step to confirm this email is yours.'
			: 'Enter the code to continue.'}
>
	<div class="space-y-4">
		<ErrorAlert message={error} />

		{#if step === 'email'}
			<Button
				variant="outline"
				class="w-full gap-2"
				onclick={() => (window.location.href = githubLoginUrl(next || undefined))}
			>
				<svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true" fill="currentColor">
					<path
						d="M12 .5C5.65.5.5 5.65.5 12a11.5 11.5 0 0 0 7.86 10.92c.58.1.79-.25.79-.56v-2c-3.2.7-3.87-1.37-3.87-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.71 1.26 3.37.96.1-.75.4-1.26.73-1.55-2.56-.29-5.25-1.28-5.25-5.7 0-1.26.45-2.29 1.19-3.1-.12-.29-.52-1.47.11-3.06 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.77.11 3.06.74.81 1.19 1.84 1.19 3.1 0 4.43-2.7 5.4-5.26 5.69.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z"
					/>
				</svg>
				Continue with GitHub
			</Button>

			<div class="flex items-center gap-3 text-xs text-muted-foreground">
				<span class="h-px flex-1 bg-border"></span>or<span class="h-px flex-1 bg-border"></span>
			</div>

			<form class="space-y-3" onsubmit={sendCode}>
				<Field label="Email" id="email">
					<Input id="email" type="email" autocomplete="email" required placeholder="you@company.com" bind:value={email} />
				</Field>
				<Button type="submit" class="w-full" disabled={busy || !email.trim()}>
					{busy ? 'Sending…' : 'Email me a sign-in code'}
				</Button>
			</form>
		{:else}
			{#if notice}<p class="text-sm text-muted-foreground">{notice}</p>{/if}
			<form class="space-y-3" onsubmit={verify}>
				<Field label={step === 'verify' ? 'Verification code' : '6-digit code'} id="code">
					<Input
						id="code"
						bind:ref={codeInput}
						inputmode="numeric"
						autocomplete="one-time-code"
						maxlength={6}
						required
						class="text-center font-mono text-lg tracking-[0.5em]"
						placeholder="••••••"
						bind:value={code}
						oninput={onCodeInput}
					/>
				</Field>
				<Button type="submit" class="w-full" disabled={busy || code.length !== 6}>
					{busy ? 'Signing in…' : 'Sign in'}
				</Button>
			</form>
			<div class="flex justify-between text-xs">
				<button type="button" class="text-muted-foreground hover:text-foreground" onclick={useDifferentEmail}>
					Use a different email
				</button>
				{#if step === 'code'}
					<button type="button" class="text-muted-foreground hover:text-foreground" disabled={busy} onclick={() => sendCode()}>
						Send a new code
					</button>
				{/if}
			</div>
		{/if}
	</div>
</AuthCard>
