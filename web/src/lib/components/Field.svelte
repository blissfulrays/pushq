<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		label,
		id,
		hint,
		children
	}: { label: string; id: string; hint?: string; children: Snippet } = $props();
</script>

<div class="space-y-1.5">
	<label for={id} class="text-sm font-medium">{label}</label>
	{@render children()}
	{#if hint}<p class="text-xs text-muted-foreground">{hint}</p>{/if}
</div>
