<script lang="ts">
	// The pushq mark, as on pushqueue.dev.
	let { size = 26, class: className = '' }: { size?: number; class?: string } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 64 64" aria-hidden="true" class={className}>
	<rect width="64" height="64" rx="16" fill="#18181b" stroke="#2e2e2e" stroke-width="1.5" />
	<path
		d="M32 47V17M19 30l13-13 13 13"
		fill="none"
		stroke="#fafafa"
		stroke-width="6.5"
		stroke-linecap="round"
		stroke-linejoin="round"
	/>
</svg>
