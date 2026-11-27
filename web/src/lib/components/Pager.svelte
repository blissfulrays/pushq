<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { PAGE_SIZES, storePageSize } from '$lib/pagination';

	let {
		page = $bindable(1),
		pageSize = $bindable(25),
		total,
		from,
		to,
		hasNext,
		storageKey,
		noun = 'rows'
	}: {
		page?: number;
		pageSize?: number;
		/** Known total (offset paging). Omit for cursor paging. */
		total?: number;
		from?: number;
		to?: number;
		/** Cursor paging: whether another page exists. */
		hasNext?: boolean;
		/** Remembers the chosen page size under this key. */
		storageKey?: string;
		noun?: string;
	} = $props();

	const pages = $derived(total === undefined ? undefined : Math.max(1, Math.ceil(total / pageSize)));
	const canPrev = $derived(page > 1);
	const canNext = $derived(pages !== undefined ? page < pages : !!hasNext);

	function setSize(e: Event) {
		pageSize = Number((e.currentTarget as HTMLSelectElement).value);
		page = 1;
		if (storageKey) storePageSize(storageKey, pageSize);
	}
</script>

<div class="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
	<div class="flex items-center gap-2">
		<label for="pager-size-{storageKey ?? noun}">Rows per page</label>
		<select
			id="pager-size-{storageKey ?? noun}"
			class="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground"
			value={pageSize}
			onchange={setSize}
		>
			{#each PAGE_SIZES as n (n)}<option value={n}>{n}</option>{/each}
		</select>
	</div>
	<div class="flex items-center gap-3">
		<span aria-live="polite">
			{#if total !== undefined}
				{total === 0 ? `No ${noun}` : `${from}–${to} of ${total}`}
			{:else}
				Page {page}
			{/if}
		</span>
		<div class="flex gap-1">
			<Button variant="outline" size="sm" disabled={!canPrev} onclick={() => (page -= 1)} aria-label="Previous page">
				Previous
			</Button>
			<Button variant="outline" size="sm" disabled={!canNext} onclick={() => (page += 1)} aria-label="Next page">
				Next
			</Button>
		</div>
	</div>
</div>
