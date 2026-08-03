<script lang="ts">
	import '../app.css';
	import type { Snippet } from 'svelte';
	import TopBar from '$lib/components/TopBar.svelte';
	import KeyDialog from '$lib/components/KeyDialog.svelte';

	let { children }: { children: Snippet } = $props();
</script>

<svelte:head>
	<title>pushq</title>
</svelte:head>

<div class="flex min-h-screen flex-col">
	<TopBar />
	<main class="mx-auto w-full max-w-5xl flex-1 px-4 py-8 sm:px-6">
		{@render children()}
	</main>
	<footer class="border-t py-4">
		<p class="mx-auto w-full max-w-5xl px-4 text-xs text-muted-foreground sm:px-6">
			pushq — self-hosted HTTP push task queue
		</p>
	</footer>
</div>

<KeyDialog />
