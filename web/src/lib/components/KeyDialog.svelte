<script lang="ts">
	import { auth } from '$lib/auth.svelte';
	import Dialog from '$lib/components/ui/dialog/dialog.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';

	let value = $state('');

	$effect(() => {
		if (auth.promptOpen) value = auth.key;
	});

	function save(e: SubmitEvent) {
		e.preventDefault();
		auth.setKey(value);
	}
</script>

<Dialog
	bind:open={auth.promptOpen}
	title="API key"
	description="Sent as Authorization: Bearer on every request. Kept in sessionStorage for this tab only."
>
	<form class="space-y-4" onsubmit={save}>
		<Input
			type="password"
			placeholder="pushq API key"
			autocomplete="off"
			bind:value
		/>
		<div class="flex justify-end gap-2">
			<Button type="button" variant="outline" onclick={() => auth.setKey('')}>Clear</Button>
			<Button type="submit">Save</Button>
		</div>
	</form>
</Dialog>
