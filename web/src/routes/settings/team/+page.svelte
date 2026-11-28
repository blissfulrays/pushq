<script lang="ts">
	import { absoluteLink, accountsApi, errorMessage, teamsApi, type Invite } from '$lib/api';
	import { auth, type Role, type User } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import DataTable from '$lib/components/data-table/DataTable.svelte';
	import type { Column } from '$lib/components/data-table/features';
	import PanelHeader from '$lib/components/settings/PanelHeader.svelte';
	import { renderSnippet } from '@tanstack/svelte-table';
	import Dialog from '$lib/components/ui/dialog/dialog.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import CopyField from '$lib/components/CopyField.svelte';
	import ErrorAlert from '$lib/components/ErrorAlert.svelte';
	import TimeStamp from '$lib/components/TimeStamp.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import { paginate, storedPageSize } from '$lib/pagination';

	const roles: { value: Role; label: string; hint: string }[] = [
		{ value: 'admin', label: 'Admin', hint: 'Everything, including team and API keys' },
		{ value: 'member', label: 'Member', hint: 'Create, pause, purge and delete queues and tasks' },
		{ value: 'viewer', label: 'Viewer', hint: 'Read-only' }
	];

	let users = $state<User[] | null>(null);
	let invites = $state<Invite[]>([]);

	let userPage = $state(1);
	let userPageSize = $state(storedPageSize('team', 25));
	const pagedUsers = $derived(paginate(users ?? [], userPage, userPageSize));
	let invitePage = $state(1);
	let invitePageSize = $state(storedPageSize('invites', 10));
	const pagedInvites = $derived(paginate(invites, invitePage, invitePageSize));
	// Keep pages in range when people are removed or invites revoked.
	$effect(() => {
		if (userPage !== pagedUsers.page) userPage = pagedUsers.page;
		if (invitePage !== pagedInvites.page) invitePage = pagedInvites.page;
	});
	let error = $state<string | null>(null);

	let inviteEmail = $state('');
	let inviteRole = $state<Role>('member');
	let inviteBusy = $state(false);
	let inviteErr = $state<string | null>(null);

	// Link dialog (new invite or password reset).
	let linkOpen = $state(false);
	let linkTitle = $state('');
	let linkDesc = $state('');
	let link = $state('');

	let teamName = $state(auth.session?.team?.name ?? '');
	let renameMsg = $state<string | null>(null);

	async function rename(e: SubmitEvent) {
		e.preventDefault();
		renameMsg = null;
		try {
			await teamsApi.renameCurrent(teamName);
			await auth.refresh();
			renameMsg = 'Saved.';
		} catch (err) {
			error = errorMessage(err);
		}
	}

	let removeTarget = $state<User | null>(null);
	let removeOpen = $state(false);
	let busy = $state(false);

	async function load() {
		try {
			const [u, i] = await Promise.all([accountsApi.listUsers(), accountsApi.listInvites()]);
			users = u.users;
			invites = i.invites;
			error = null;
		} catch (e) {
			error = errorMessage(e);
		}
	}

	$effect(() => {
		load();
	});

	function showLink(title: string, desc: string, path: string) {
		linkTitle = title;
		linkDesc = desc;
		link = path ? absoluteLink(path) : '';
		linkOpen = true;
	}

	async function sendInvite(e: SubmitEvent) {
		e.preventDefault();
		inviteBusy = true;
		inviteErr = null;
		try {
			const res = await accountsApi.createInvite(inviteEmail, inviteRole);
			if (res.emailed || !res.path) {
				showLink(
					'Invitation sent',
					`We emailed ${res.invite.email} an invitation to join as ${res.invite.role}. They'll appear here once they accept.`,
					''
				);
			} else {
				showLink(
					'Invite link created',
					`Send this link to ${res.invite.email}. It works once and expires in 7 days.`,
					res.path
				);
			}
			inviteEmail = '';
			await load();
		} catch (err) {
			inviteErr = errorMessage(err);
		} finally {
			inviteBusy = false;
		}
	}

	async function changeRole(u: User, role: Role) {
		error = null;
		try {
			await accountsApi.updateUser(u.id, { role });
			if (u.id === auth.user?.id) await auth.refresh();
		} catch (e) {
			error = errorMessage(e);
		}
		await load();
	}

	async function resetLink(u: User) {
		error = null;
		try {
			const res = await accountsApi.resetLink(u.id);
			showLink(
				'Password reset link',
				`Send this to ${u.email}. It works once, expires in 24 hours, and signs them out everywhere.`,
				res.path
			);
		} catch (e) {
			error = errorMessage(e);
		}
	}

	async function remove() {
		if (!removeTarget) return;
		busy = true;
		try {
			await accountsApi.deleteUser(removeTarget.id);
			const self = removeTarget.id === auth.user?.id;
			removeOpen = false;
			if (self) await auth.refresh();
			else await load();
		} catch (e) {
			error = errorMessage(e);
			removeOpen = false;
		} finally {
			busy = false;
		}
	}

	const userColumns: Column<User>[] = [
		{ id: 'member', header: 'Member', cell: ({ row }) => renderSnippet(memberCell, row.original) },
		{ id: 'role', header: 'Role', cell: ({ row }) => renderSnippet(roleCell, row.original) },
		{
			id: 'last',
			header: 'Last sign-in',
			class: 'hidden text-muted-foreground sm:table-cell',
			cell: ({ row }) => renderSnippet(lastSignInCell, row.original)
		},
		{
			id: 'actions',
			header: 'Actions',
			class: 'text-right',
			cell: ({ row }) => renderSnippet(memberActionsCell, row.original)
		}
	];

	const inviteColumns: Column<Invite>[] = [
		{ accessorKey: 'email', header: 'Email', class: 'font-medium' },
		{ accessorKey: 'role', header: 'Role', class: 'capitalize text-muted-foreground' },
		{
			id: 'expires',
			header: 'Expires',
			class: 'hidden text-muted-foreground sm:table-cell',
			cell: ({ row }) => renderSnippet(expiresCell, row.original)
		},
		{
			id: 'actions',
			header: '',
			class: 'text-right',
			cell: ({ row }) => renderSnippet(inviteActionsCell, row.original)
		}
	];

	async function revoke(inv: Invite) {
		try {
			await accountsApi.deleteInvite(inv.id);
			await load();
		} catch (e) {
			error = errorMessage(e);
		}
	}
</script>

<svelte:head>
	<title>Team · pushq</title>
</svelte:head>

<PanelHeader
	title={auth.saas ? 'Team' : 'Team members'}
	description={auth.saas
		? 'Everyone in the team can work with all of its applications, with the role you give them.'
		: 'People who can sign in to this server, and what they may do.'}
/>

<div class="mt-6 space-y-6">
	<ErrorAlert message={error} />

	{#if auth.saas}
		<Card.Root>
			<Card.Header>
				<Card.Title>Team name</Card.Title>
				<Card.Description>Everyone in the team sees this in the team switcher.</Card.Description>
			</Card.Header>
			<Card.Content>
				<form class="flex flex-col gap-2 sm:flex-row sm:items-center" onsubmit={rename}>
					<Input required maxlength={100} class="sm:max-w-sm" bind:value={teamName} />
					<Button type="submit" variant="secondary">Save</Button>
					{#if renameMsg}<span class="text-sm text-muted-foreground">{renameMsg}</span>{/if}
				</form>
			</Card.Content>
		</Card.Root>
	{/if}

	<Card.Root>
		<Card.Header>
			<Card.Title>Invite a team member</Card.Title>
			<Card.Description>
				{auth.saas ? "We'll email them an invitation." : "You'll get a link to send them — pushq doesn't send email."}
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form class="flex flex-col gap-2 sm:flex-row" onsubmit={sendInvite}>
				<Input type="email" placeholder="name@company.com" required class="sm:flex-1" bind:value={inviteEmail} />
				<select
					bind:value={inviteRole}
					class="h-9 rounded-md border border-input bg-background px-2 text-sm"
					aria-label="Role"
				>
					{#each roles as r (r.value)}<option value={r.value}>{r.label}</option>{/each}
				</select>
				<Button type="submit" disabled={inviteBusy}>
					{inviteBusy ? 'Sending…' : auth.saas ? 'Send invite' : 'Create invite link'}
				</Button>
			</form>
			<ErrorAlert message={inviteErr} class="mt-3" />
			<ul class="mt-3 space-y-0.5 text-xs text-muted-foreground">
				{#each roles as r (r.value)}<li><span class="font-medium text-foreground">{r.label}</span> — {r.hint}</li>{/each}
			</ul>
		</Card.Content>
	</Card.Root>

	<DataTable
		data={users === null ? null : pagedUsers.rows}
		columns={userColumns}
		getRowId={(u) => String(u.id)}
		empty="No members yet."
	/>
	{#if users && users.length > 0}
		<Pager
			bind:page={userPage}
			bind:pageSize={userPageSize}
			total={pagedUsers.total}
			from={pagedUsers.from}
			to={pagedUsers.to}
			storageKey="team"
			noun="members"
		/>
	{/if}

	{#if invites.length > 0}
		<div class="space-y-2">
			<h2 class="text-sm font-semibold">Pending invites</h2>
			<DataTable data={pagedInvites.rows} columns={inviteColumns} getRowId={(inv) => String(inv.id)} />
			<Pager
				bind:page={invitePage}
				bind:pageSize={invitePageSize}
				total={pagedInvites.total}
				from={pagedInvites.from}
				to={pagedInvites.to}
				storageKey="invites"
				noun="invites"
			/>
		</div>
	{/if}
</div>

<Dialog bind:open={linkOpen} title={linkTitle} description={linkDesc}>
	{#if link}<CopyField value={link} label="Copy link" />{/if}
	{#snippet footer()}
		<Button onclick={() => (linkOpen = false)}>Done</Button>
	{/snippet}
</Dialog>

<ConfirmDialog
	bind:open={removeOpen}
	title={removeTarget?.id === auth.user?.id ? 'Remove yourself?' : `Remove ${removeTarget?.email ?? ''}?`}
	description="They lose access immediately and are signed out everywhere. You can invite them again later."
	confirmLabel="Remove"
	{busy}
	onconfirm={remove}
/>

{#snippet memberCell(u: User)}
	<p class="font-medium">
		{u.name || u.email}
		{#if u.id === auth.user?.id}<span class="ml-1 text-xs font-normal text-muted-foreground">(you)</span>{/if}
	</p>
	{#if u.name}<p class="text-xs text-muted-foreground">{u.email}</p>{/if}
{/snippet}

{#snippet roleCell(u: User)}
	<select
		value={u.role}
		onchange={(e) => changeRole(u, e.currentTarget.value as Role)}
		class="h-8 rounded-md border border-input bg-background px-2 text-sm"
		aria-label={`Role for ${u.email}`}
	>
		{#each roles as r (r.value)}<option value={r.value}>{r.label}</option>{/each}
	</select>
{/snippet}

{#snippet lastSignInCell(u: User)}
	{#if u.last_login_at}<TimeStamp value={u.last_login_at} />{:else}never{/if}
{/snippet}

{#snippet memberActionsCell(u: User)}
	<div class="flex justify-end gap-1">
		{#if !auth.saas}
			<Button variant="ghost" size="sm" onclick={() => resetLink(u)}>Reset password</Button>
		{/if}
		<Button
			variant="ghost"
			size="sm"
			class="text-destructive hover:text-destructive"
			onclick={() => {
				removeTarget = u;
				removeOpen = true;
			}}>Remove</Button
		>
	</div>
{/snippet}

{#snippet expiresCell(inv: Invite)}
	expires <TimeStamp value={inv.expires_at} />
{/snippet}

{#snippet inviteActionsCell(inv: Invite)}
	<Button variant="ghost" size="sm" onclick={() => revoke(inv)}>Revoke</Button>
{/snippet}
