/** Client-side paging for lists already held in memory. */
export interface PageSlice<T> {
	rows: T[];
	/** 1-based, clamped into range (a deletion can shrink the last page away). */
	page: number;
	pages: number;
	total: number;
	/** 1-based index of the first row shown (0 when empty). */
	from: number;
	to: number;
}

export function paginate<T>(items: T[], page: number, size: number): PageSlice<T> {
	const total = items.length;
	const pages = Math.max(1, Math.ceil(total / size));
	const p = Math.min(Math.max(1, page), pages);
	const start = (p - 1) * size;
	const rows = items.slice(start, start + size);
	return { rows, page: p, pages, total, from: total ? start + 1 : 0, to: start + rows.length };
}

export const PAGE_SIZES = [10, 25, 50, 100] as const;

/** Remembered rows-per-page per table (a per-browser convenience). */
export function storedPageSize(key: string, fallback: number): number {
	try {
		const v = Number(localStorage.getItem(`pushq_page_size:${key}`));
		return (PAGE_SIZES as readonly number[]).includes(v) ? v : fallback;
	} catch {
		return fallback;
	}
}

export function storePageSize(key: string, size: number) {
	try {
		localStorage.setItem(`pushq_page_size:${key}`, String(size));
	} catch {
		/* storage unavailable */
	}
}
