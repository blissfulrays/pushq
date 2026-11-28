<script lang="ts">
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { auth } from '$lib/auth.svelte';

	// /settings opens the first section this caller can see.
	$effect(() => {
		const s = auth.session;
		if (!s) return;
		goto(`${base}/settings/${s.user ? 'account' : 'apps'}`, { replaceState: true });
	});
</script>
