<script lang="ts">
	import { Button } from '$lib/components/ui/button';

	let { value, label = 'Copy' }: { value: string; label?: string } = $props();
	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(value);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* clipboard blocked: the text stays selectable */
		}
	}
</script>

<div class="flex items-center gap-2">
	<input
		readonly
		{value}
		class="h-9 min-w-0 flex-1 rounded-md border border-input bg-muted px-3 font-mono text-xs"
		onfocus={(e) => e.currentTarget.select()}
	/>
	<Button variant="outline" size="sm" onclick={copy}>{copied ? 'Copied' : label}</Button>
</div>
