<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		descriptionSnippet,
		action
	}: {
		title: string;
		description?: string;
		/** Rich description (links, inline code); replaces `description`. */
		descriptionSnippet?: Snippet;
		/** The section's main button, on the right. */
		action?: Snippet;
	} = $props();
</script>

<div class="flex flex-wrap items-center gap-3 border-b pb-5">
	<div class="min-w-0">
		<h2 class="text-[22px] font-semibold tracking-[-0.04em]">{title}</h2>
		{#if descriptionSnippet}
			<p class="mt-0.5 text-sm text-muted-foreground">{@render descriptionSnippet()}</p>
		{:else if description}
			<p class="mt-0.5 text-sm text-muted-foreground">{description}</p>
		{/if}
	</div>
	{#if action}
		<div class="ml-auto">{@render action()}</div>
	{/if}
</div>
