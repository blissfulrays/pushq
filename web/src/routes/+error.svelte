<script lang="ts">
	import { page } from '$app/state';
	import { base } from '$app/paths';
</script>

<div class="py-24 text-center">
	<p class="text-4xl font-semibold tabular-nums">{page.status}</p>
	<p class="mt-2 text-sm text-muted-foreground">{page.error?.message ?? 'Something went wrong'}</p>
	<a class="mt-6 inline-block text-sm underline underline-offset-4" href="{base}/">Back to queues</a>
</div>
