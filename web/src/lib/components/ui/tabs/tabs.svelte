<script lang="ts">
	import { cn } from '$lib/utils';

	let {
		value,
		options,
		onValueChange,
		class: className = ''
	}: {
		value: string;
		options: { value: string; label: string }[];
		onValueChange?: (value: string) => void;
		class?: string;
	} = $props();
</script>

<div
	role="tablist"
	class={cn(
		'inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground',
		className
	)}
>
	{#each options as option (option.value)}
		<button
			type="button"
			role="tab"
			aria-selected={value === option.value}
			class={cn(
				'inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
				value === option.value
					? 'bg-background text-foreground shadow-sm'
					: 'hover:text-foreground/80'
			)}
			onclick={() => onValueChange?.(option.value)}
		>
			{option.label}
		</button>
	{/each}
</div>
