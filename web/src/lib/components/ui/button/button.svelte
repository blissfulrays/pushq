<script lang="ts" module>
	export const buttonVariants = {
		variant: {
			default: 'bg-primary text-primary-foreground hover:bg-primary/80',
			destructive: 'bg-destructive/10 text-destructive hover:bg-destructive/20',
			outline: 'border border-input bg-transparent hover:border-foreground/30 hover:bg-muted',
			secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/70',
			ghost: 'text-muted-foreground hover:bg-muted hover:text-foreground',
			link: 'text-primary underline-offset-4 hover:underline'
		},
		size: {
			default: 'h-9 px-3.5',
			sm: 'h-8 px-3 text-[13px]',
			lg: 'h-10 px-6',
			icon: 'h-9 w-9'
		}
	} as const;

	export type ButtonVariant = keyof typeof buttonVariants.variant;
	export type ButtonSize = keyof typeof buttonVariants.size;
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLButtonAttributes } from 'svelte/elements';
	import { cn } from '$lib/utils';

	let {
		class: className = '',
		variant = 'default',
		size = 'default',
		type = 'button',
		children,
		...restProps
	}: HTMLButtonAttributes & {
		variant?: ButtonVariant;
		size?: ButtonSize;
		children?: Snippet;
	} = $props();
</script>

<button
	{type}
	class={cn(
		'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-lg text-sm font-medium transition-colors active:translate-y-px focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
		buttonVariants.variant[variant],
		buttonVariants.size[size],
		className
	)}
	{...restProps}
>
	{@render children?.()}
</button>
