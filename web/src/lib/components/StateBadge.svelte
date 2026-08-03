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
		'queue:running': 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400',
		'queue:paused': 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
		'queue:disabled': 'bg-red-500/15 text-red-700 dark:text-red-400',
		'task:pending': 'bg-sky-500/15 text-sky-700 dark:text-sky-400',
		'task:running': 'bg-blue-500/15 text-blue-700 dark:text-blue-400',
		'task:succeeded': 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400',
		'task:failed': 'bg-red-500/15 text-red-700 dark:text-red-400'
	};

	const cls = $derived(styles[`${kind}:${state ?? ''}`] ?? 'bg-muted text-muted-foreground');
</script>

<span
	class={cn('inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium', cls, className)}
>
	{state ?? 'unknown'}
</span>
