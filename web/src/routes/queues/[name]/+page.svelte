<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, errorMessage, type Queue, type QueueStats, type Task } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { appContext } from '$lib/app-context.svelte';
	import { apps } from '$lib/apps.svelte';
	import { absoluteTime, duration, num, truncateMiddle } from '$lib/format';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import DataTable from '$lib/components/data-table/DataTable.svelte';
	import type { Column } from '$lib/components/data-table/features';
	import { renderSnippet } from '@tanstack/svelte-table';
	import Tabs from '$lib/components/ui/tabs/tabs.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import FolderCrumbs from '$lib/components/FolderCrumbs.svelte';
	import { folderOf, leafOf, queueHref } from '$lib/folders';
	import StatCard from '$lib/components/StatCard.svelte';
	import StateBadge from '$lib/components/StateBadge.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import { storedPageSize } from '$lib/pagination';
	import { untrack } from 'svelte';

	const name = $derived(page.params.name ?? '');

	let queue = $state<Queue | null>(null);
	let stats = $state<QueueStats | null>(null);
	let tasks = $state<Task[] | null>(null);
	let nextToken = $state('');
	let pageToken = $state('');
	// Tasks page by server cursor. pageTokens[i] is the cursor for page i+1,
	// so Previous can step back.
	let pageNum = $state(1);
	let pageSize = $state(storedPageSize('tasks', 50));
	let pageTokens: string[] = [''];
	let stateFilter = $state('');
	let loadError = $state<string | null>(null);
	let actionError = $state<string | null>(null);
	let purgeOpen = $state(false);
	let busy = $state(false);

	const filterOptions = [
		{ value: '', label: 'All' },
		{ value: 'pending', label: 'Pending' },
		{ value: 'running', label: 'Running' },
		{ value: 'succeeded', label: 'Succeeded' },
		{ value: 'failed', label: 'Failed' }
	];

	async function loadMeta(q: string) {
		try {
			const [qRes, sRes] = await Promise.all([api.getQueue(q), api.getQueueStats(q)]);
			queue = qRes;
			stats = sRes;
			loadError = null;
		} catch (e) {
			loadError = errorMessage(e);
		}
	}

	async function loadTasks(q: string, filter: string, token: string, size: number) {
		try {
			const res = await api.listTasks(q, {
				state: filter || undefined,
				pageSize: size,
				pageToken: token || undefined
			});
			tasks = res.tasks ?? [];
			nextToken = res.next_page_token ?? '';
		} catch (e) {
			loadError = errorMessage(e);
		}
	}

	$effect(() => {
		auth.key;
		appContext.id;
		if (apps.list === null) return; // wait until the application is known
		const q = name;
		if (!q) return;
		queue = null;
		stats = null;
		loadMeta(q);
		const timer = setInterval(() => loadMeta(q), 5000);
		return () => clearInterval(timer);
	});

	$effect(() => {
		auth.key;
		appContext.id;
		if (apps.list === null) return; // wait until the application is known
		const q = name;
		const filter = stateFilter;
		const token = pageToken;
		const size = pageSize;
		if (!q) return;
		tasks = null;
		nextToken = ''; // no stale "Next" while the new page loads
		loadTasks(q, filter, token, size);
		const timer = setInterval(() => loadTasks(q, filter, token, size), 5000);
		return () => clearInterval(timer);
	});

	function setFilter(value: string) {
		stateFilter = value;
		resetPaging();
	}

	function resetPaging() {
		pageTokens = [''];
		pageNum = 1;
		pageToken = '';
	}

	// Moving forward records the cursor the server handed back; moving back
	// reuses a recorded one.
	$effect.pre(() => {
		const p = pageNum;
		untrack(() => {
			if (p > pageTokens.length) pageTokens = [...pageTokens, nextToken];
			pageToken = pageTokens[p - 1] ?? '';
		});
	});

	// A new page size or a different queue starts again from the first page.
	let lastKey = '';
	$effect.pre(() => {
		const key = `${name}|${pageSize}`;
		untrack(() => {
			if (key !== lastKey) {
				if (lastKey) resetPaging();
				lastKey = key;
			}
		});
	});

	const taskColumns: Column<Task>[] = [
		{
			id: 'id',
			header: 'ID',
			class: 'font-mono text-xs',
			cell: ({ row }) => renderSnippet(idCell, row.original)
		},
		{ id: 'state', header: 'State', cell: ({ row }) => renderSnippet(taskStateCell, row.original) },
		{
			id: 'scheduled',
			header: 'Scheduled',
			class: 'text-muted-foreground',
			cell: ({ row }) => renderSnippet(scheduledCell, row.original)
		},
		{
			id: 'dispatches',
			header: 'Dispatches',
			class: 'text-right tabular-nums',
			cell: ({ row }) => num(row.original.dispatch_count)
		},
		{
			id: 'last',
			header: 'Last attempt',
			class: 'max-w-56',
			cell: ({ row }) => renderSnippet(lastAttemptCell, row.original)
		}
	];

	async function togglePause() {
		if (!queue) return;
		busy = true;
		actionError = null;
		try {
			queue =
				queue.state === 'running' ? await api.pauseQueue(name) : await api.resumeQueue(name);
		} catch (e) {
			actionError = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	async function doPurge() {
		busy = true;
		actionError = null;
		try {
			queue = await api.purgeQueue(name);
			purgeOpen = false;
			loadMeta(name);
			loadTasks(name, stateFilter, pageToken, pageSize);
		} catch (e) {
			actionError = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	function openTask(id: string) {
		goto(`${queueHref(name)}/tasks/${encodeURIComponent(id)}`);
	}

	const targetSummary = $derived.by(() => {
		const t = queue?.target;
		if (!t || (!t.base_url && !t.method)) return '—';
		return [t.method, t.base_url].filter(Boolean).join(' ');
	});

	const retrySummary = $derived.by(() => {
		const r = queue?.retry;
		if (!r) return '—';
		const attempts = r.max_attempts === -1 ? 'unlimited attempts' : `${r.max_attempts} attempts`;
		const dur =
			r.max_retry_duration_seconds && r.max_retry_duration_seconds > 0
				? ` · ≤ ${duration(r.max_retry_duration_seconds)}`
				: '';
		return attempts + dur;
	});

	const backoffSummary = $derived.by(() => {
		const r = queue?.retry;
		if (!r) return '—';
		return `${duration(r.min_backoff_seconds)} – ${duration(r.max_backoff_seconds)} · ${r.max_doublings ?? '—'} doublings`;
	});

	const rateSummary = $derived.by(() => {
		const rl = queue?.rate_limits;
		if (!rl) return '—';
		return `${num(rl.max_dispatches_per_second)}/s · burst ${num(rl.max_burst_size)}`;
	});
</script>

<svelte:head>
	<title>{name} · pushq</title>
</svelte:head>

{#snippet configRow(label: string, value: string, mono = false)}
	<div class="flex items-baseline justify-between gap-6">
		<dt class="shrink-0 text-muted-foreground">{label}</dt>
		<dd
			class={mono ? 'truncate text-right font-mono text-xs' : 'truncate text-right font-medium'}
			title={value}
		>
			{value}
		</dd>
	</div>
{/snippet}

<div class="space-y-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div class="min-w-0">
			<FolderCrumbs folder={folderOf(name)} />
			<div class="mt-1 flex items-center gap-3">
				<h1 class="truncate font-mono text-xl font-semibold tracking-tight" title={name}>
					{leafOf(name)}
				</h1>
				{#if queue}
					<StateBadge kind="queue" state={queue.state} />
				{/if}
			</div>
		</div>
		{#if auth.can.write}
			<div class="flex items-center gap-2">
				{#if queue}
					<Button variant="outline" size="sm" disabled={busy} onclick={togglePause}>
						{queue.state === 'running' ? 'Pause' : 'Resume'}
					</Button>
				{/if}
				<Button variant="destructive" size="sm" disabled={busy} onclick={() => (purgeOpen = true)}>
					Purge
				</Button>
			</div>
		{/if}
	</div>

	<ErrorAlert message={actionError} />
	<ErrorAlert message={loadError} />

	<div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
		<StatCard label="Pending" value={num(stats?.pending)} />
		<StatCard label="Running" value={num(stats?.running)} />
		<StatCard label="Succeeded" value={num(stats?.succeeded)} />
		<StatCard label="Failed" value={num(stats?.failed)} />
		<StatCard label="Scheduled" value={num(stats?.scheduled_future)} sub="due in the future" />
		<StatCard
			label="Oldest due"
			value={stats?.oldest_due_age_seconds != null ? duration(stats.oldest_due_age_seconds) : '—'}
			sub={stats?.oldest_due_at ? absoluteTime(stats.oldest_due_at) : undefined}
		/>
	</div>

	<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
		<StatCard
			label="Dispatched"
			value={`${num(stats?.dispatched_last_1m)} last minute`}
			sub={`${num(stats?.dispatched_last_1h)} last hour`}
		/>
		<StatCard
			label="Errors"
			value={`${num(stats?.errors_last_1m)} last minute`}
			sub={`${num(stats?.errors_last_1h)} last hour`}
		/>
	</div>

	{#if queue}
		<Card.Root>
			<Card.Header>
				<Card.Title>Configuration</Card.Title>
				<Card.Description>Target, rate limits and retry policy.</Card.Description>
			</Card.Header>
			<Card.Content>
				<dl class="grid grid-cols-1 gap-x-10 gap-y-2.5 text-sm sm:grid-cols-2">
					{@render configRow('State', queue.state ?? '—')}
					{@render configRow('Target', targetSummary, true)}
					{@render configRow(
						'Signing secret',
						queue.target?.has_signing_secret ? 'configured' : 'none'
					)}
					<div class="flex items-baseline justify-between gap-6">
						<dt class="shrink-0 text-muted-foreground">Dead-letter queue</dt>
						<dd class="truncate text-right font-medium">
							{#if queue.dead_letter_queue}
								<a
									class="font-mono text-xs underline underline-offset-4"
									href={queueHref(queue.dead_letter_queue)}
								>
									{queue.dead_letter_queue}
								</a>
							{:else}
								none
							{/if}
						</dd>
					</div>
					{@render configRow('Rate limit', rateSummary)}
					{@render configRow('Concurrency', num(queue.rate_limits?.max_concurrent_dispatches))}
					{@render configRow('Retry', retrySummary)}
					{@render configRow('Backoff', backoffSummary)}
					{@render configRow('Created', absoluteTime(queue.created_at) || '—')}
					{@render configRow('Updated', absoluteTime(queue.updated_at) || '—')}
					{#if queue.purged_at}
						{@render configRow('Last purge', absoluteTime(queue.purged_at))}
					{/if}
				</dl>
			</Card.Content>
		</Card.Root>
	{/if}

	<div class="space-y-3">
		<div class="flex flex-wrap items-center justify-between gap-3">
			<h2 class="text-base font-semibold tracking-tight">Tasks</h2>
			<Tabs value={stateFilter} options={filterOptions} onValueChange={setFilter} />
		</div>

		<DataTable
			data={tasks}
			columns={taskColumns}
			getRowId={(t) => t.id}
			onRowClick={(t) => openTask(t.id)}
			rowLabel={(t) => `Task ${t.id}`}
			empty={`No ${stateFilter || ''} tasks.`}
		/>

		<Pager bind:page={pageNum} bind:pageSize hasNext={!!nextToken} storageKey="tasks" noun="tasks" />
	</div>
</div>

<ConfirmDialog
	bind:open={purgeOpen}
	title="Purge queue"
	description="Purge deletes every task created up to now, including in-flight ones. Their results are discarded and their IDs become reusable. Tasks created after the purge are unaffected."
	confirmLabel="Purge"
	{busy}
	onconfirm={doPurge}
/>

{#snippet idCell(t: Task)}
	<span title={t.id}>{truncateMiddle(t.id, 26)}</span>
{/snippet}

{#snippet taskStateCell(t: Task)}
	<StateBadge state={t.state} />
{/snippet}

{#snippet scheduledCell(t: Task)}
	<TimeStamp value={t.schedule_time} />
{/snippet}

{#snippet lastAttemptCell(t: Task)}
	{#if t.last_attempt && (t.last_attempt.status || t.last_attempt.error)}
		<div class="flex items-center gap-2">
			{#if t.last_attempt.status}
				<span
					class="font-mono text-xs {t.last_attempt.status < 400
						? 'text-success'
						: 'text-destructive'}"
				>
					{t.last_attempt.status}
				</span>
			{/if}
			{#if t.last_attempt.error}
				<span class="truncate text-xs text-muted-foreground" title={t.last_attempt.error}>
					{t.last_attempt.error}
				</span>
			{/if}
		</div>
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}
