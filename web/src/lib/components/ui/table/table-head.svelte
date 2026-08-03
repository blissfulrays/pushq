<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLThAttributes } from 'svelte/elements';
	import { cn } from '$lib/utils';

	let {
		class: className = '',
		children,
		...restProps
	}: HTMLThAttributes & { children?: Snippet } = $props();
</script>

<th
	class={cn(
		'h-10 px-3 text-left align-middle font-medium text-muted-foreground',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</th>
