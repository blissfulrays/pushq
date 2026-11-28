<script lang="ts">
	import type { Snippet } from 'svelte';

	// A name tag that slides out to the right of a rail item on hover/focus.
	let { children }: { children: Snippet } = $props();
</script>

<span
	class="pointer-events-none absolute left-[calc(100%+12px)] top-1/2 z-50 -translate-x-1 -translate-y-1/2 whitespace-nowrap rounded-md border bg-popover px-2 py-1 text-xs font-medium text-popover-foreground opacity-0 shadow-xl transition-[opacity,translate] duration-150 group-focus-within/tip:translate-x-0 group-focus-within/tip:opacity-100 group-hover/tip:translate-x-0 group-hover/tip:opacity-100 motion-reduce:transition-none"
>
	{@render children()}
</span>
