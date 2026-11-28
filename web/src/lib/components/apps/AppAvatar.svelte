<script lang="ts">
	import { appHue, appInitials } from '$lib/apps.svelte';
	import { cn } from '$lib/utils';

	let {
		id,
		name,
		icon,
		class: className = ''
	}: { id: number; name: string; icon?: string; class?: string } = $props();
</script>

<!-- The application's icon, or its initials on a tile tinted per application. -->
{#if icon}
	<img
		src={icon}
		alt=""
		class={cn('size-8 shrink-0 select-none rounded-lg object-cover ring-1 ring-inset ring-edge', className)}
	/>
{:else}
	<span
		class={cn(
			'app-tile grid size-8 shrink-0 select-none place-items-center rounded-lg font-mono text-[11px] font-semibold',
			className
		)}
		style="--h: {appHue(id)}"
		aria-hidden="true">{appInitials(name)}</span
	>
{/if}
