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

<div class="rounded-xl border bg-card p-4 text-card-foreground shadow-sm">
	<p class="text-xs font-medium text-muted-foreground">{label}</p>
	<p class="mt-1 text-2xl font-semibold tabular-nums tracking-tight">{value}</p>
	{#if sub}
		<p class="mt-0.5 text-xs tabular-nums text-muted-foreground">{sub}</p>
	{/if}
</div>
