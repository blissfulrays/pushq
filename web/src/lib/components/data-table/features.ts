import { columnVisibilityFeature, tableFeatures, type ColumnDef, type RowData } from '@tanstack/svelte-table';

/**
 * TanStack Table v9 feature set shared by every <DataTable>. v9 is
 * tree-shakeable: only registered features add state and APIs. Visibility
 * gives `row.getVisibleCells()`. Paging, sorting and filtering happen on the
 * server (or in the page), so no row models beyond the core one are needed.
 */
export const features = tableFeatures({ columnVisibilityFeature });

export type Features = typeof features;

/** A column plus the classes its header and body cells get. */
export type Column<T extends RowData> = ColumnDef<Features, T> & {
	/** Applied to header and body cells (e.g. `text-right`, `hidden md:table-cell`). */
	class?: string;
	/** Applied to the header cell only, after `class`. */
	headerClass?: string;
};
