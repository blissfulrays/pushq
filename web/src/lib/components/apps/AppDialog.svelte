<script lang="ts">
	import ImagePlusIcon from '@lucide/svelte/icons/image-plus';
	import { appsApi, errorMessage, type App } from '$lib/api';
	import { iconFromFile } from '$lib/image';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import Dialog from '$lib/components/ui/dialog/dialog.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import AppAvatar from './AppAvatar.svelte';

	/** Creates an application, or edits `app` (name and icon) when given. */
	let {
		open = $bindable(false),
		app = null,
		onsaved
	}: { open?: boolean; app?: App | null; onsaved?: (app: App) => void } = $props();

	let name = $state('');
	let icon = $state('');
	let busy = $state(false);
	let reading = $state(false);
	let error = $state<string | null>(null);
	let input = $state<HTMLInputElement | null>(null);
	let file = $state<HTMLInputElement | null>(null);

	// Fresh form each time it opens.
	$effect(() => {
		if (open) {
			name = app?.name ?? '';
			icon = app?.icon ?? '';
			error = null;
			requestAnimationFrame(() => input?.focus());
		}
	});

	async function pick(e: Event) {
		const f = (e.currentTarget as HTMLInputElement).files?.[0];
		(e.currentTarget as HTMLInputElement).value = ''; // same file can be picked again
		if (!f) return;
		reading = true;
		error = null;
		try {
			icon = await iconFromFile(f);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			reading = false;
		}
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = null;
		try {
			let saved: App;
			if (app) {
				const patch: { name?: string; icon?: string } = {};
				if (name.trim() !== app.name) patch.name = name;
				if (icon !== (app.icon ?? '')) patch.icon = icon;
				saved = Object.keys(patch).length ? await appsApi.update(app.id, patch) : app;
			} else {
				saved = await appsApi.create(name, icon);
			}
			open = false;
			onsaved?.(saved);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog
	bind:open
	title={app ? 'Edit application' : 'New application'}
	description={app
		? undefined
		: 'Applications keep queues apart: each has its own queues, and API keys can be limited to one.'}
>
	<form id="app-form" onsubmit={save} class="space-y-4">
		<div class="flex items-center gap-4">
			<AppAvatar id={app?.id ?? 0} name={name.trim() || '?'} icon={icon || undefined} class="size-14 rounded-xl text-sm" />
			<div class="flex flex-wrap gap-2">
				<Button variant="outline" size="sm" disabled={reading} onclick={() => file?.click()}>
					<ImagePlusIcon />{reading ? 'Reading…' : icon ? 'Change icon' : 'Upload icon'}
				</Button>
				{#if icon}
					<Button variant="ghost" size="sm" onclick={() => (icon = '')}>Remove</Button>
				{/if}
				<p class="w-full font-mono text-[11px] text-subtle">PNG, JPEG, WebP or GIF · cropped to a square</p>
			</div>
			<input
				bind:this={file}
				type="file"
				accept="image/png,image/jpeg,image/webp,image/gif"
				class="hidden"
				onchange={pick}
			/>
		</div>
		<label class="block space-y-1.5">
			<span class="text-sm font-medium">Name</span>
			<Input
				bind:ref={input}
				bind:value={name}
				placeholder="e.g. Billing, Notifications"
				maxlength={60}
				required
			/>
		</label>
		<ErrorAlert message={error} />
	</form>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="app-form" disabled={busy || reading || !name.trim()}>
			{busy ? 'Saving…' : app ? 'Save' : 'Create application'}
		</Button>
	{/snippet}
</Dialog>
