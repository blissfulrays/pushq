<script lang="ts">
	import type { Component, Snippet } from 'svelte';
	import { cn } from '$lib/utils';

	let {
		title,
		description,
		icon: Icon,
		danger = false,
		lead,
		body,
		ctl,
		class: className = ''
	}: {
		title?: string;
		description?: string;
		/** Lucide icon drawn in the tile on the left. */
		icon?: Component<{ class?: string; strokeWidth?: number }>;
		/** Red tile, for destructive rows. */
		danger?: boolean;
		/** Replaces the icon tile, e.g. with an avatar. */
		lead?: Snippet;
		/** Replaces the title/description block. */
		body?: Snippet;
		/** Controls on the right. */
		ctl?: Snippet;
		class?: string;
	} = $props();
</script>

<div class={cn('group/row flex flex-wrap items-center gap-4 border-b py-5 last:border-b-0 sm:flex-nowrap', className)}>
	{#if lead}
		{@render lead()}
	{:else if Icon}
		<span
			class={cn(
				'grid size-10 flex-none place-items-center self-start rounded-[11px] transition-transform duration-300 group-hover/row:-rotate-3 group-hover/row:scale-[1.04]',
				danger ? 'bg-destructive/10 text-destructive' : 'border bg-background text-foreground/80 shadow-sm'
			)}
		>
			<Icon class="size-[18px]" strokeWidth={1.7} />
		</span>
	{/if}
	<div class="min-w-0 flex-1">
		{#if body}
			{@render body()}
		{:else}
			<b class="block text-[15px] font-medium">{title}</b>
			{#if description}
				<span class="mt-0.5 block max-w-md text-[13.5px] text-muted-foreground">{description}</span>
			{/if}
		{/if}
	</div>
	{#if ctl}
		<div class="flex flex-none items-center gap-2">{@render ctl()}</div>
	{/if}
</div>
