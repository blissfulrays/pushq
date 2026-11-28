<script lang="ts">
	let {
		label,
		value,
		sub
	}: {
		label: string;
		value: string;
		sub?: string;
	} = $props();
</script>

<div class="rounded-xl border bg-card p-4 text-card-foreground">
	<p class="font-mono text-xs text-subtle">{label}</p>
	<p class="mt-1.5 text-2xl font-semibold tabular-nums tracking-[-0.04em]">{value}</p>
	{#if sub}
		<p class="mt-0.5 text-xs tabular-nums text-muted-foreground">{sub}</p>
	{/if}
</div>
