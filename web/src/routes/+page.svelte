<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { api, ApiError, errorMessage, type Queue } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import * as Table from '$lib/components/ui/table';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import StateBadge from '$lib/components/StateBadge.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';

	let prefix = $state('');
	let queues = $state<Queue[] | null>(null);
	let error = $state<string | null>(null);
	let forbidden = $state(false);
	let directName = $state('');

	const createHint = 'No queues yet — create one with PUT /v1/queues/{name}.';

	async function load(p: string) {
		try {
			const res = await api.listQueues({ namePrefix: p, pageSize: 100 });
			queues = res.queues ?? [];
			forbidden = false;
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 403) {
				forbidden = true;
				error = null;
			} else {
				error = errorMessage(e);
			}
		}
	}

	let first = true;
	$effect(() => {
		auth.key; // refetch when the key changes
		const p = prefix;
		const delay = first ? 0 : 300;
		first = false;
		const debounce = setTimeout(() => load(p), delay);
		const timer = setInterval(() => load(p), 5000);
		return () => {
			clearTimeout(debounce);
			clearInterval(timer);
		};
	});

	function openQueue(name: string) {
		goto(`${base}/queues/${encodeURIComponent(name)}`);
	}

	function openDirect(e: SubmitEvent) {
		e.preventDefault();
		const n = directName.trim();
		if (n) openQueue(n);
	}
</script>

<svelte:head>
	<title>Queues · pushq</title>
</svelte:head>

<div class="space-y-6">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-tight">Queues</h1>
			<p class="mt-1 text-sm text-muted-foreground">Everything this server is dispatching.</p>
		</div>
		{#if !forbidden}
			<Input class="w-full sm:w-64" placeholder="Filter by name prefix…" bind:value={prefix} />
		{/if}
	</div>

	<ErrorAlert message={error} />

	{#if forbidden}
		<Card.Root>
			<Card.Header>
				<Card.Title>Admin key required</Card.Title>
				<Card.Description>
					Listing queues needs an admin API key — you can still open a queue directly by name.
				</Card.Description>
			</Card.Header>
			<Card.Content>
				<form class="flex max-w-sm gap-2" onsubmit={openDirect}>
					<Input placeholder="queue name" bind:value={directName} />
					<Button type="submit" variant="secondary">Open</Button>
				</form>
			</Card.Content>
		</Card.Root>
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
					{:else if queues.length === 0}
						<Table.Row>
							<Table.Cell colspan={3} class="h-16 text-center text-muted-foreground">
								{prefix ? `No queues matching “${prefix}”.` : createHint}
							</Table.Cell>
						</Table.Row>
					{:else}
						{#each queues as q (q.name)}
							<Table.Row
								class="cursor-pointer"
								role="link"
								tabindex={0}
								onclick={() => openQueue(q.name)}
								onkeydown={(e) => {
									if (e.key === 'Enter') openQueue(q.name);
								}}
							>
								<Table.Cell class="font-mono text-[13px] font-medium">{q.name}</Table.Cell>
								<Table.Cell><StateBadge kind="queue" state={q.state} /></Table.Cell>
								<Table.Cell class="text-right text-muted-foreground">
									<TimeStamp value={q.created_at} />
								</Table.Cell>
							</Table.Row>
						{/each}
					{/if}
				</Table.Body>
			</Table.Root>
		</div>
	{/if}
</div>
