<script lang="ts">
	import { crumbs, folderHref } from '$lib/folders';

	let {
		folder,
		linkLast = true
	}: {
		/** Folder path, e.g. "user/instagram" ("" = root). */
		folder: string;
		/** Whether the last crumb is a link (false when it is the current page). */
		linkLast?: boolean;
	} = $props();

	const items = $derived(crumbs(folder));
</script>

<nav aria-label="Folder" class="flex min-w-0 flex-wrap items-center gap-1 text-xs text-muted-foreground">
	{#if items.length === 0 && !linkLast}
		<span class="text-foreground">All queues</span>
	{:else}
		<a href={folderHref('')} class="hover:text-foreground">All queues</a>
	{/if}
	{#each items as c, i (c.path)}
		<span aria-hidden="true">/</span>
		{#if i === items.length - 1 && !linkLast}
			<span class="font-mono text-foreground" aria-current="page">{c.label}</span>
		{:else}
			<a href={folderHref(c.path)} class="font-mono hover:text-foreground">{c.label}</a>
		{/if}
	{/each}
</nav>
