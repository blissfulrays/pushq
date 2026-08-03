<script lang="ts">
	import { absoluteTime, relativeTime } from '$lib/format';

	let {
		value,
		class: className = ''
	}: {
		value?: string | null;
		class?: string;
	} = $props();
</script>

{#if value}
	<span class={className} title={absoluteTime(value)}>{relativeTime(value)}</span>
{:else}
	<span class={className}>—</span>
{/if}
