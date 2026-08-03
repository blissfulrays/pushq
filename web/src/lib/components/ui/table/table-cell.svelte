<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLTdAttributes } from 'svelte/elements';
	import { cn } from '$lib/utils';

	let {
		class: className = '',
		children,
		...restProps
	}: HTMLTdAttributes & { children?: Snippet } = $props();
</script>

<td class={cn('px-3 py-2.5 align-middle', className)} {...restProps}>
	{@render children?.()}
</td>
