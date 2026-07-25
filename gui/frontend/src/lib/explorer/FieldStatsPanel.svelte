<script lang="ts">
  // E8: the inline stats panel for one sidebar field. Owns its own fetch +
  // loading/error/not-found state (TreeNode only decides WHETHER to mount it).
  // TreeNode mounts one panel per field with a FIXED `path` and destroys it on
  // collapse, so `path` never changes while mounted -- there is no cross-column
  // race to guard (unlike E6's single shared overlay), and a response that lands
  // after $destroy() is a silent no-op in Svelte 3. So: fetch once on mount.
  import { onMount } from "svelte";
  import { explorer } from "./store";
  import FieldDetail from "../FieldDetail.svelte";
  import type { FieldCard } from "./types";

  export let path: string;

  let loading = true;
  let error = "";
  let found = true;
  let card: FieldCard | null = null;

  onMount(async () => {
    try {
      const res = await explorer.getColumnStats(path);
      card = res.card;
      found = res.found;
      loading = false;
    } catch (e) {
      error = String(e);
      loading = false;
    }
  });
</script>

<div class="field-stats" role="region" aria-label="Statistics for {path}">
  {#if loading}
    <p class="stats-loading">Loading…</p>
  {:else if error}
    <p class="stats-error" role="alert">{error}</p>
  {:else if !found || !card}
    <p class="stats-empty">No statistics for this column.</p>
  {:else}
    <FieldDetail {card} />
  {/if}
</div>

<style>
  .field-stats {
    padding: var(--space-2) var(--space-3);
    border-top: 1px solid var(--border);
    background: var(--surface-1);
  }
  .stats-loading, .stats-empty { margin: 0; font-size: 12px; color: var(--text-muted); }
  .stats-error {
    margin: 0; font-size: 12px; color: var(--status-critical);
    background: var(--status-critical-bg); padding: var(--space-2); border-radius: var(--radius-sm);
  }
</style>
