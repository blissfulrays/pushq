<script lang="ts">
	import { base } from '$app/paths';
	import LogoMark from './LogoMark.svelte';
	import ThemeToggle from './ThemeToggle.svelte';

	// Just the mark and the theme switch: navigation, the account menu and
	// the application switcher live in the rail on the left.
</script>

<header class="sticky top-0 z-30 border-b bg-background/70 backdrop-blur-md backdrop-saturate-[1.8]">
	<div class="mx-auto flex h-14 w-full max-w-5xl items-center gap-5 px-4 sm:px-6">
		<a href="{base}/" class="flex items-center gap-2.5" aria-label="pushq home">
			<LogoMark />
			<span class="text-lg font-semibold tracking-[-0.04em]">pushq</span>
		</a>
		<div class="ml-auto">
			<ThemeToggle />
		</div>
	</div>
</header>
