<script lang="ts">
	import * as Alert from '$lib/components/ui/alert';

	let {
		message,
		class: className = ''
	}: {
		message: string | null | undefined;
		class?: string;
	} = $props();
</script>

{#if message}
	<Alert.Root variant="destructive" class={className}>
		<Alert.Description>{message}</Alert.Description>
	</Alert.Root>
{/if}
