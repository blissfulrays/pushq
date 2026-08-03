<script lang="ts">
	import type { Snippet } from 'svelte';
	import { fade, scale } from 'svelte/transition';
	import { cn } from '$lib/utils';

	let {
		open = $bindable(false),
		title,
		description,
		class: className = '',
		children,
		footer
	}: {
		open?: boolean;
		title: string;
		description?: string;
		class?: string;
		children?: Snippet;
		footer?: Snippet;
	} = $props();

	function onKeydown(e: KeyboardEvent) {
		if (open && e.key === 'Escape') open = false;
	}
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
	<button
		type="button"
		aria-label="Close dialog"
		class="fixed inset-0 z-40 h-full w-full cursor-default bg-black/60 backdrop-blur-[2px]"
		transition:fade={{ duration: 120 }}
		onclick={() => (open = false)}
	></button>
	<div
		role="dialog"
		aria-modal="true"
		aria-label={title}
		tabindex="-1"
		class={cn(
			'fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-xl border bg-background p-6 shadow-lg outline-none',
			className
		)}
		transition:scale={{ start: 0.96, duration: 140 }}
	>
		<h2 class="text-lg font-semibold leading-none tracking-tight">{title}</h2>
		{#if description}
			<p class="mt-2 text-sm text-muted-foreground">{description}</p>
		{/if}
		{#if children}
			<div class="mt-4">{@render children()}</div>
		{/if}
		{#if footer}
			<div class="mt-6 flex justify-end gap-2">{@render footer()}</div>
		{/if}
	</div>
{/if}
