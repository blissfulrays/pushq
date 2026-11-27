<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import FolderCrumbs from '$lib/components/FolderCrumbs.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import { paginate, storedPageSize } from '$lib/pagination';
	import { folderOf, leafOf, queueHref } from '$lib/folders';
	import { api, errorMessage, type Attempt, type Task } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { absoluteTime, decodeBody, duration, millis, num, truncateMiddle } from '$lib/format';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Table from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import StateBadge from '$lib/components/StateBadge.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';

	const qname = $derived(page.params.name ?? '');
	const taskId = $derived(page.params.id ?? '');

	let task = $state<Task | null>(null);
	let attempts = $state<Attempt[] | null>(null);
	let attemptPage = $state(1);
	let attemptPageSize = $state(storedPageSize('attempts', 25));
	const pagedAttempts = $derived(paginate(attempts ?? [], attemptPage, attemptPageSize));
	$effect(() => {
		if (attemptPage !== pagedAttempts.page) attemptPage = pagedAttempts.page;
	});
	let loadError = $state<string | null>(null);
	let actionError = $state<string | null>(null);
	let notice = $state<string | null>(null);
	let deleteOpen = $state(false);
	let busy = $state(false);

	async function load(q: string, id: string) {
		try {
			const [tRes, aRes] = await Promise.all([
				api.getTask(q, id, 'full'),
				api.listAttempts(q, id)
			]);
			task = tRes;
			attempts = aRes.attempts ?? [];
			loadError = null;
		} catch (e) {
			loadError = errorMessage(e);
		}
	}

	$effect(() => {
		auth.key;
		const q = qname;
		const id = taskId;
		if (!q || !id) return;
		task = null;
		attempts = null;
		load(q, id);
		const timer = setInterval(() => load(q, id), 5000);
		return () => clearInterval(timer);
	});

	async function runNow() {
		busy = true;
		actionError = null;
		try {
			task = await api.runTask(qname, taskId);
			notice = 'Dispatch triggered.';
			setTimeout(() => (notice = null), 3000);
			load(qname, taskId);
		} catch (e) {
			actionError = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	async function doDelete() {
		busy = true;
		actionError = null;
		try {
			await api.deleteTask(qname, taskId);
			deleteOpen = false;
			goto(queueHref(qname));
		} catch (e) {
			actionError = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	const body = $derived(decodeBody(task?.body_base64));
	const headerEntries = $derived(Object.entries(task?.headers ?? {}));
	const retryEntries = $derived(
		Object.entries(task?.retry ?? {}).filter(([, v]) => v !== undefined && v !== null)
	);
</script>

<svelte:head>
	<title>{truncateMiddle(taskId, 18)} · {qname} · pushq</title>
</svelte:head>

{#snippet infoRow(label: string, value: string, mono = false)}
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
			<div class="flex min-w-0 flex-wrap items-center gap-1 text-xs text-muted-foreground">
				<FolderCrumbs folder={folderOf(qname)} />
				<span aria-hidden="true">/</span>
				<a href={queueHref(qname)} class="font-mono hover:text-foreground">{leafOf(qname)}</a>
			</div>
			<div class="mt-1 flex items-center gap-3">
				<h1 class="break-all font-mono text-lg font-semibold tracking-tight" title={taskId}>
					{truncateMiddle(taskId, 40)}
				</h1>
				{#if task}
					<StateBadge state={task.state} />
				{/if}
			</div>
		</div>
		{#if auth.can.write}
			<div class="flex items-center gap-2">
				<Button variant="outline" size="sm" disabled={busy || !task} onclick={runNow}>Run now</Button>
				<Button
					variant="destructive"
					size="sm"
					disabled={busy || !task}
					onclick={() => (deleteOpen = true)}
				>
					Delete
				</Button>
			</div>
		{/if}
	</div>

	<ErrorAlert message={actionError} />
	<ErrorAlert message={loadError} />

	{#if notice}
		<div
			class="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-700 dark:text-emerald-400"
		>
			{notice}
		</div>
	{/if}

	{#if task}
		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<Card.Root>
				<Card.Header>
					<Card.Title>Request</Card.Title>
					<Card.Description>What gets dispatched to your endpoint.</Card.Description>
				</Card.Header>
				<Card.Content class="space-y-4">
					<p class="break-all font-mono text-xs">
						<span class="font-semibold">{task.method ?? 'POST'}</span>
						<span class="text-muted-foreground"> {task.url ?? '—'}</span>
					</p>

					{#if headerEntries.length > 0}
						<div>
							<p class="mb-1.5 text-xs font-medium text-muted-foreground">Headers</p>
							<div class="overflow-hidden rounded-lg border">
								<table class="w-full text-xs">
									<tbody>
										{#each headerEntries as [k, v] (k)}
											<tr class="border-b last:border-0">
												<td class="w-1/3 break-all border-r px-2.5 py-1.5 font-mono text-muted-foreground">
													{k}
												</td>
												<td class="break-all px-2.5 py-1.5 font-mono">{v}</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</div>
					{/if}

					{#if body.kind !== 'empty'}
						<div>
							<p class="mb-1.5 text-xs font-medium text-muted-foreground">
								Body
								{#if body.kind === 'json'}(JSON, {num(body.bytes)} bytes){:else if body.kind === 'text'}({num(body.bytes)} bytes){/if}
							</p>
							{#if body.kind === 'binary'}
								<p class="text-xs text-muted-foreground">
									Binary body — {num(body.bytes)} bytes (not shown).
								</p>
							{:else}
								<pre
									class="max-h-72 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs leading-relaxed">{body.text}</pre>
							{/if}
						</div>
					{/if}
				</Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header>
					<Card.Title>Execution</Card.Title>
					<Card.Description>Scheduling and delivery state.</Card.Description>
				</Card.Header>
				<Card.Content>
					<dl class="grid grid-cols-1 gap-y-2.5 text-sm">
						{@render infoRow('State', task.state ?? '—')}
						{@render infoRow(
							'Scheduled',
							task.schedule_time ? absoluteTime(task.schedule_time) : '—'
						)}
						{@render infoRow('Created', task.created_at ? absoluteTime(task.created_at) : '—')}
						{@render infoRow(
							'Dispatch deadline',
							task.dispatch_deadline_seconds != null
								? duration(task.dispatch_deadline_seconds)
								: '—'
						)}
						{@render infoRow('Dispatches', num(task.dispatch_count))}
						{@render infoRow('Responses', num(task.response_count))}
						{#if task.first_attempt_at}
							{@render infoRow('First attempt', absoluteTime(task.first_attempt_at))}
						{/if}
						{#if task.finished_at}
							{@render infoRow('Finished', absoluteTime(task.finished_at))}
						{/if}
						{#if task.last_attempt}
							{@render infoRow(
								'Last attempt',
								[
									task.last_attempt.at ? absoluteTime(task.last_attempt.at) : '',
									task.last_attempt.status ? `HTTP ${task.last_attempt.status}` : '',
									task.last_attempt.error ?? ''
								]
									.filter(Boolean)
									.join(' · ') || '—'
							)}
						{/if}
					</dl>

					{#if retryEntries.length > 0}
						<div class="mt-5 border-t pt-4">
							<p class="mb-2 text-xs font-medium text-muted-foreground">Retry override</p>
							<dl class="grid grid-cols-1 gap-y-2 text-sm">
								{#each retryEntries as [k, v] (k)}
									{@render infoRow(k.replaceAll('_', ' '), String(v), true)}
								{/each}
							</dl>
						</div>
					{/if}
				</Card.Content>
			</Card.Root>
		</div>

		<Card.Root>
			<Card.Header>
				<Card.Title>Attempts</Card.Title>
				<Card.Description>Delivery history, newest first.</Card.Description>
			</Card.Header>
			<Card.Content class="px-0 pb-0">
				<Table.Root>
					<Table.Header>
						<Table.Row>
							<Table.Head class="pl-6">#</Table.Head>
							<Table.Head>Dispatched</Table.Head>
							<Table.Head>Status</Table.Head>
							<Table.Head>Error kind</Table.Head>
							<Table.Head class="text-right">Duration</Table.Head>
							<Table.Head class="pr-6">Error snippet</Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						{#if attempts === null}
							<Table.Row>
								<Table.Cell colspan={6} class="h-16 text-center text-muted-foreground">
									Loading…
								</Table.Cell>
							</Table.Row>
						{:else if attempts.length === 0}
							<Table.Row>
								<Table.Cell colspan={6} class="h-16 text-center text-muted-foreground">
									No attempts yet.
								</Table.Cell>
							</Table.Row>
						{:else}
							{#each pagedAttempts.rows as a (a.number)}
								<Table.Row>
									<Table.Cell class="pl-6 tabular-nums">{num(a.number)}</Table.Cell>
									<Table.Cell class="text-muted-foreground">
										<TimeStamp value={a.dispatch_time} />
									</Table.Cell>
									<Table.Cell>
										{#if a.response_status}
											<span
												class="font-mono text-xs {a.response_status < 400
													? 'text-emerald-600 dark:text-emerald-400'
													: 'text-red-600 dark:text-red-400'}"
											>
												{a.response_status}
											</span>
										{:else}
											<span class="text-muted-foreground">—</span>
										{/if}
									</Table.Cell>
									<Table.Cell>
										{#if a.error_kind}
											<span class="font-mono text-xs">{a.error_kind}</span>
										{:else}
											<span class="text-muted-foreground">—</span>
										{/if}
									</Table.Cell>
									<Table.Cell class="text-right tabular-nums">{millis(a.duration_ms)}</Table.Cell>
									<Table.Cell class="max-w-64 pr-6">
										{#if a.error_snippet}
											<details>
												<summary
													class="cursor-pointer text-xs text-muted-foreground hover:text-foreground"
												>
													show
												</summary>
												<pre
													class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap rounded-md bg-muted p-2 font-mono text-xs">{a.error_snippet}</pre>
											</details>
										{:else}
											<span class="text-muted-foreground">—</span>
										{/if}
									</Table.Cell>
								</Table.Row>
							{/each}
						{/if}
					</Table.Body>
				</Table.Root>
				{#if attempts && attempts.length > 0}
					<div class="border-t px-6 py-3">
						<Pager
							bind:page={attemptPage}
							bind:pageSize={attemptPageSize}
							total={pagedAttempts.total}
							from={pagedAttempts.from}
							to={pagedAttempts.to}
							storageKey="attempts"
							noun="attempts"
						/>
					</div>
				{/if}
			</Card.Content>
		</Card.Root>
	{:else if !loadError}
		<p class="text-sm text-muted-foreground">Loading…</p>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete task"
	description="The task is removed. Its ID stays reserved for the dedup window before it can be reused."
	confirmLabel="Delete"
	{busy}
	onconfirm={doDelete}
/>
