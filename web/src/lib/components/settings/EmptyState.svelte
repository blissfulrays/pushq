<script lang="ts">
	import type { Component, Snippet } from 'svelte';

	let {
		icon: Icon,
		title,
		children
	}: { icon: Component<{ class?: string; strokeWidth?: number }>; title: string; children?: Snippet } = $props();
</script>

<div class="flex flex-col items-center gap-1 py-12 text-center text-[13.5px] text-muted-foreground">
	<span class="mb-2 grid size-10 place-items-center rounded-[11px] border bg-background text-foreground/80 shadow-sm">
		<Icon class="size-[18px]" strokeWidth={1.7} />
	</span>
	<b class="text-base font-medium text-foreground">{title}</b>
	{@render children?.()}
</div>
