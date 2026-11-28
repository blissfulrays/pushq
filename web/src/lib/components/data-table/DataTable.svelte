<script lang="ts" generics="T extends RowData">
	import { createTable, FlexRender, type RowData } from '@tanstack/svelte-table';
	import type { Snippet } from 'svelte';
	import * as Table from '$lib/components/ui/table';
	import { cn } from '$lib/utils';
	import { features, type Column } from './features';

	let {
		data,
		columns,
		loading = false,
		empty = 'Nothing here yet.',
		emptySnippet,
		getRowId,
		onRowClick,
		rowLabel,
		class: className = ''
	}: {
		/** null while the first load is in flight. */
		data: T[] | null;
		columns: Column<T>[];
		loading?: boolean;
		empty?: string;
		/** Replaces `empty` when there are no rows. */
		emptySnippet?: Snippet;
		/** A stable id per row, so rows keep their DOM across refreshes. */
		getRowId?: (row: T) => string;
		/** Makes rows links: clickable and Enter-activatable. */
		onRowClick?: (row: T) => void;
		/** Accessible name for a clickable row. */
		rowLabel?: (row: T) => string;
		class?: string;
	} = $props();

	const table = createTable({
		features,
		get data() {
			return data ?? [];
		},
		get columns() {
			return columns;
		},
		get getRowId() {
			return getRowId && ((row: T) => getRowId(row));
		}
	});

	const colOf = (c: unknown) => c as Column<T>;
	const span = $derived(columns.length);
</script>

<div class={cn('rounded-xl border', className)}>
	<Table.Root>
		<Table.Header>
			{#each table.getHeaderGroups() as group (group.id)}
				<Table.Row>
					{#each group.headers as header (header.id)}
						{@const col = colOf(header.column.columnDef)}
						<Table.Head class={cn(col.class, col.headerClass)}>
							{#if !header.isPlaceholder}<FlexRender {header} />{/if}
						</Table.Head>
					{/each}
				</Table.Row>
			{/each}
		</Table.Header>
		<Table.Body>
			{#if data === null || (loading && data.length === 0)}
				<Table.Row>
					<Table.Cell colspan={span} class="h-16 text-center text-muted-foreground">Loading…</Table.Cell>
				</Table.Row>
			{:else if data.length === 0}
				<Table.Row>
					<Table.Cell colspan={span} class="h-16 text-center text-muted-foreground">
						{#if emptySnippet}{@render emptySnippet()}{:else}{empty}{/if}
					</Table.Cell>
				</Table.Row>
			{:else}
				{#each table.getRowModel().rows as row (row.id)}
					<Table.Row
						class={onRowClick ? 'cursor-pointer' : ''}
						role={onRowClick ? 'link' : undefined}
						tabindex={onRowClick ? 0 : undefined}
						aria-label={onRowClick && rowLabel ? rowLabel(row.original) : undefined}
						onclick={onRowClick ? () => onRowClick(row.original) : undefined}
						onkeydown={onRowClick
							? (e: KeyboardEvent) => {
									if (e.key === 'Enter') onRowClick(row.original);
								}
							: undefined}
					>
						{#each row.getVisibleCells() as cell (cell.id)}
							<Table.Cell class={colOf(cell.column.columnDef).class}>
								<FlexRender {cell} />
							</Table.Cell>
						{/each}
					</Table.Row>
				{/each}
			{/if}
		</Table.Body>
	</Table.Root>
</div>
