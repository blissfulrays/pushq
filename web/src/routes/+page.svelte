<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { page } from '$app/state';
	import { api, ApiError, errorMessage, type Queue } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { folderHref, groupByFolder, queueHref, type FolderRow, type Row } from '$lib/folders';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import * as Table from '$lib/components/ui/table';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import FolderCrumbs from '$lib/components/FolderCrumbs.svelte';
	import StateBadge from '$lib/components/StateBadge.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import { paginate, storedPageSize } from '$lib/pagination';
	import { cn } from '$lib/utils';

	/** Upper bound on queues fetched for one folder view (pages of 1000). */
	const MAX_QUEUES = 5000;

	// Current folder ("" = root), normalized: no leading/trailing slashes.
	const folder = $derived(
		(page.url.searchParams.get('folder') ?? '').replace(/^\/+|\/+$/g, '')
	);

	let filter = $state('');
	let pageNum = $state(1);
	let pageSize = $state(storedPageSize('queues', 50));
	let queues = $state<Queue[] | null>(null);
	let truncated = $state(false);
	let error = $state<string | null>(null);
	let forbidden = $state(false);
	let directName = $state('');


	// Responses can arrive out of order (polling + typing + folder changes);
	// only the latest request may write state.
	let seq = 0;

	async function load(namePrefix: string) {
		const mine = ++seq;
		try {
			const all: Queue[] = [];
			let token: string | undefined;
			do {
				const res = await api.listQueues({ namePrefix, pageSize: 1000, pageToken: token });
				all.push(...(res.queues ?? []));
				token = res.next_page_token || undefined;
			} while (token && all.length < MAX_QUEUES);
			if (mine !== seq) return;
			queues = all;
			truncated = !!token;
			forbidden = false;
			error = null;
		} catch (e) {
			if (mine !== seq) return;
			if (e instanceof ApiError && e.status === 403) {
				forbidden = true;
				error = null;
			} else {
				error = errorMessage(e);
			}
		}
	}

	// Clear the filter and stale rows when moving between folders.
	let lastFolder: string | undefined;
	$effect.pre(() => {
		if (folder !== lastFolder) {
			if (lastFolder !== undefined) {
				filter = '';
				queues = null;
				pageNum = 1;
			}
			lastFolder = folder;
		}
	});

	const folderPrefix = $derived(folder ? folder + '/' : '');
	const searching = $derived(filter.trim() !== '');
	// Nothing anywhere yet (not just an empty folder or search): show
	// onboarding instead of an empty table.
	const firstRun = $derived(queues !== null && queues.length === 0 && !searching && !folder);
	const canCreateKeys = $derived(auth.can.manage && !!auth.user);
	const linkButton = (variant: 'default' | 'outline') =>
		cn(
			'inline-flex h-9 items-center justify-center gap-2 whitespace-nowrap rounded-md px-4 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring [&_svg]:size-4',
			buttonVariants.variant[variant]
		);

	let lastTyped: string | undefined;
	$effect(() => {
		auth.key; // refetch when the key changes
		const typed = filter.trim();
		const p = folderPrefix + typed;
		// Debounce typing only; first load and folder changes fetch at once.
		const delay = lastTyped !== undefined && typed !== lastTyped ? 300 : 0;
		lastTyped = typed;
		const debounce = setTimeout(() => load(p), delay);
		const timer = setInterval(() => load(p), 5000);
		return () => {
			clearTimeout(debounce);
			clearInterval(timer);
		};
	});

	// Browsing: immediate subfolders + queues in this folder. Searching: a flat
	// list of every matching queue, labelled relative to the current folder.
	const rows = $derived.by<Row[]>(() => {
		if (!queues) return [];
		if (searching) {
			return queues.map((q) => ({
				kind: 'queue' as const,
				label: q.name.slice(folderPrefix.length),
				queue: q
			}));
		}
		return groupByFolder(queues, folder);
	});

	const paged = $derived(paginate(rows, pageNum, pageSize));

	// Back to the first page whenever the search changes.
	let lastFilter = '';
	$effect.pre(() => {
		const f = filter.trim();
		if (f !== lastFilter) {
			lastFilter = f;
			pageNum = 1;
		}
	});
	// Keep the page in range when the list shrinks (e.g. queues deleted).
	$effect(() => {
		if (pageNum !== paged.page) pageNum = paged.page;
	});

	function open(row: Row) {
		goto(row.kind === 'folder' ? folderHref(row.path) : queueHref(row.queue.name));
	}

	function openDirect(e: SubmitEvent) {
		e.preventDefault();
		const n = directName.trim();
		if (n) goto(queueHref(n));
	}

	function folderSummary(f: FolderRow) {
		const parts = [`${f.queues} queue${f.queues === 1 ? '' : 's'}`];
		if (f.paused) parts.push(`${f.paused} paused`);
		if (f.disabled) parts.push(`${f.disabled} disabled`);
		return parts.join(', ');
	}
</script>

<svelte:head>
	<title>{folder ? `${folder} · ` : ''}Queues · pushq</title>
</svelte:head>

<div class="space-y-6">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div class="min-w-0">
			{#if folder}
				<FolderCrumbs {folder} linkLast={false} />
				<h1 class="mt-1 truncate font-mono text-xl font-semibold tracking-tight">{folder}/</h1>
			{:else}
				<h1 class="text-xl font-semibold tracking-tight">Queues</h1>
				<p class="mt-1 text-sm text-muted-foreground">Everything this server is dispatching.</p>
			{/if}
		</div>
		{#if !forbidden && !firstRun}
			<Input
				class="w-full sm:w-64"
				placeholder={folder ? `Filter in ${folder}/…` : 'Filter by name prefix…'}
				bind:value={filter}
			/>
		{/if}
	</div>

	<ErrorAlert message={error} />

	{#if forbidden}
		<Card.Root>
			<Card.Header>
				<Card.Title>Listing queues isn't allowed for this key</Card.Title>
				<Card.Description>
					Writer API keys can't list queues — you can still open a queue directly by name.
				</Card.Description>
			</Card.Header>
			<Card.Content>
				<form class="flex max-w-sm gap-2" onsubmit={openDirect}>
					<Input placeholder="queue name, e.g. user/instagram" bind:value={directName} />
					<Button type="submit" variant="secondary">Open</Button>
				</form>
			</Card.Content>
		</Card.Root>
	{:else if firstRun}
		<div
			class="flex min-h-[50vh] flex-col items-center justify-center rounded-xl border border-dashed px-6 py-16 text-center"
		>
			<div class="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
				<svg
					class="size-6"
					viewBox="0 0 24 24"
					fill="none"
					stroke="currentColor"
					stroke-width="2"
					stroke-linecap="round"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<path d="m15.5 7.5 2.3 2.3a1 1 0 0 0 1.4 0l2.1-2.1a1 1 0 0 0 0-1.4L19 4" />
					<path d="m21 2-9.6 9.6" />
					<circle cx="7.5" cy="15.5" r="5.5" />
				</svg>
			</div>
			<h2 class="mt-4 text-lg font-semibold tracking-tight">No queues yet</h2>
			<p class="mt-2 max-w-md text-sm text-muted-foreground">
				{canCreateKeys ? 'Create an API key' : 'Get an API key from a team admin'}, then create queues and
				tasks from your app. Name a queue like
				<code class="font-mono text-xs">user/instagram</code> to group it in a folder.
			</p>
			<div class="mt-6 flex flex-wrap justify-center gap-2">
				{#if canCreateKeys}
					<a href="{base}/settings/api-keys" class={linkButton('default')}>Create API key</a>
				{/if}
				<a href="/docs" data-sveltekit-reload class={linkButton('outline')}>API docs</a>
			</div>
		</div>
	{:else}
		<div class="rounded-xl border">
			<Table.Root>
				<Table.Header>
					<Table.Row>
						<Table.Head>Name</Table.Head>
						<Table.Head>State</Table.Head>
						<Table.Head class="text-right">Created</Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#if queues === null}
						<Table.Row>
							<Table.Cell colspan={3} class="h-16 text-center text-muted-foreground">
								Loading…
							</Table.Cell>
						</Table.Row>
					{:else if rows.length === 0}
						<Table.Row>
							<Table.Cell colspan={3} class="h-16 text-center text-muted-foreground">
								{#if searching}
									No queues matching “{folderPrefix}{filter.trim()}”.
								{:else if folder}
									No queues in {folder}/.
								{:else}
									No queues yet.
								{/if}
							</Table.Cell>
						</Table.Row>
					{:else}
						{#each paged.rows as row (row.kind === 'folder' ? `f:${row.path}` : `q:${row.queue.name}`)}
							<Table.Row
								class="cursor-pointer"
								role="link"
								tabindex={0}
								onclick={() => open(row)}
								onkeydown={(e) => {
									if (e.key === 'Enter') open(row);
								}}
							>
								{#if row.kind === 'folder'}
									<Table.Cell class="font-mono text-[13px] font-medium">
										<span class="inline-flex items-center gap-2">
											<svg
												class="size-4 shrink-0 text-muted-foreground"
												viewBox="0 0 24 24"
												fill="none"
												stroke="currentColor"
												stroke-width="2"
												stroke-linecap="round"
												stroke-linejoin="round"
												aria-hidden="true"
											>
												<path
													d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"
												/>
											</svg>
											{row.label}/
										</span>
									</Table.Cell>
									<Table.Cell class="text-xs text-muted-foreground">{folderSummary(row)}</Table.Cell>
									<Table.Cell></Table.Cell>
								{:else}
									<Table.Cell class="font-mono text-[13px] font-medium" title={row.queue.name}>
										{row.label}
									</Table.Cell>
									<Table.Cell><StateBadge kind="queue" state={row.queue.state} /></Table.Cell>
									<Table.Cell class="text-right text-muted-foreground">
										<TimeStamp value={row.queue.created_at} />
									</Table.Cell>
								{/if}
							</Table.Row>
						{/each}
					{/if}
				</Table.Body>
			</Table.Root>
		</div>
		{#if queues !== null && rows.length > 0}
			<Pager
				bind:page={pageNum}
				bind:pageSize
				total={paged.total}
				from={paged.from}
				to={paged.to}
				storageKey="queues"
				noun="queues"
			/>
		{/if}
		{#if truncated}
			<p class="text-xs text-muted-foreground">
				Showing the first {MAX_QUEUES} queues{folder ? ` in ${folder}/` : ''} — open a folder or filter
				to narrow it down.
			</p>
		{/if}
	{/if}
</div>
