<script lang="ts">
	import { cn } from '$lib/utils';

	let {
		state,
		kind = 'task',
		class: className = ''
	}: {
		state?: string;
		kind?: 'queue' | 'task';
		class?: string;
	} = $props();

	const styles: Record<string, string> = {
		'queue:running': 'border-success/25 bg-success/10 text-success',
		'queue:paused': 'border-warning/25 bg-warning/10 text-warning',
		'queue:disabled': 'border-destructive/25 bg-destructive/10 text-destructive',
		'task:pending': 'border-edge bg-muted text-muted-foreground',
		'task:running': 'border-brand/25 bg-brand/10 text-brand',
		'task:succeeded': 'border-success/25 bg-success/10 text-success',
		'task:failed': 'border-destructive/25 bg-destructive/10 text-destructive'
	};

	const cls = $derived(styles[`${kind}:${state ?? ''}`] ?? 'border-edge bg-muted text-muted-foreground');
	// A live dot for states that are moving.
	const live = $derived(state === 'running');
</script>

<span
	class={cn(
		'inline-flex items-center gap-1.5 rounded-full border px-2 py-px font-mono text-[11px] font-medium',
		cls,
		className
	)}
>
	<i class={cn('size-1.5 rounded-full bg-current', live && 'animate-pulse')}></i>
	{state ?? 'unknown'}
</span>
