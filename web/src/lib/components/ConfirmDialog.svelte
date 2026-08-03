<script lang="ts">
	import Dialog from '$lib/components/ui/dialog/dialog.svelte';
	import { Button } from '$lib/components/ui/button';

	let {
		open = $bindable(false),
		title,
		description,
		confirmLabel = 'Confirm',
		destructive = true,
		busy = false,
		onconfirm
	}: {
		open?: boolean;
		title: string;
		description?: string;
		confirmLabel?: string;
		destructive?: boolean;
		busy?: boolean;
		onconfirm: () => void;
	} = $props();
</script>

<Dialog bind:open {title} {description}>
	{#snippet footer()}
		<Button variant="outline" disabled={busy} onclick={() => (open = false)}>Cancel</Button>
		<Button variant={destructive ? 'destructive' : 'default'} disabled={busy} onclick={onconfirm}>
			{busy ? 'Working…' : confirmLabel}
		</Button>
	{/snippet}
</Dialog>
