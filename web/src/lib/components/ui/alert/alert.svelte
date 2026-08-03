<script lang="ts" module>
	export const alertVariants = {
		default: 'bg-background text-foreground',
		destructive: 'border-destructive/50 text-destructive dark:border-destructive/60'
	} as const;

	export type AlertVariant = keyof typeof alertVariants;
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn } from '$lib/utils';

	let {
		class: className = '',
		variant = 'default',
		children,
		...restProps
	}: HTMLAttributes<HTMLDivElement> & {
		variant?: AlertVariant;
		children?: Snippet;
	} = $props();
</script>

<div
	role="alert"
	class={cn('relative w-full rounded-lg border px-4 py-3 text-sm', alertVariants[variant], className)}
	{...restProps}
>
	{@render children?.()}
</div>
