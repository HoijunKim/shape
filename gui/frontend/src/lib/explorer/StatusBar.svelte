<script lang="ts">
  // The explorer's bottom status bar (T8, spec §4): the sole place the app
  // ever states a row/column count, so it is the sole place the honesty
  // rules (never present an estimate as exact; never reword the streaming-
  // mode warning) get enforced. Every prop here is a plain, already-computed
  // value read straight off $explorer by Explorer.svelte -- this component
  // owns no store subscription of its own.
  import { formatRowCount } from "./rowCount";

  export let tier = "";
  export let total = -1;
  export let totalExact = false;
  export let sampled = false;
  export let rowsLoaded = false;
  export let columnCount = 0;
  export let columnsTruncated = false;
  export let totalPaths = 0;
  export let warnings: string[] = [];
  export let fetching = false;

  $: rowsText = formatRowCount({ total, totalExact, rowsLoaded });
  $: columnsText = columnsTruncated
    ? `showing ${columnCount.toLocaleString()} of ${totalPaths.toLocaleString()} columns`
    : `${columnCount.toLocaleString()} column${columnCount === 1 ? "" : "s"}`;
</script>

<div class="status-bar">
  <div class="metrics" role="status">
    <span class="metric mono" class:estimate={sampled && !totalExact}>{rowsText}</span>
    <span class="sep" aria-hidden="true">·</span>
    <span class="metric mono">{columnsText}</span>
    {#if tier}
      <span class="sep" aria-hidden="true">·</span>
      <span class="tier">{tier}</span>
    {/if}
    {#if fetching}
      <span class="pip" aria-hidden="true"></span>
      <span class="loading">loading…</span>
    {/if}
  </div>

  <!-- Obligation carried from earlier reviews: when the source is sampled,
       the warnings strings render VERBATIM -- in particular the streaming-
       mode string, which a Go test matches byte-for-byte, so it is never
       reworded or wrapped in extra punctuation here. -->
  {#if sampled && warnings.length > 0}
    <div class="warnings" role="note">
      {#each warnings as w (w)}
        <span class="warning">{w}</span>
      {/each}
    </div>
  {/if}
</div>

<style>
  .status-bar {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-4);
    padding: var(--space-1) var(--space-4);
    min-height: 30px;
    box-sizing: border-box;
    font-size: 11px;
    color: var(--text-muted);
    background: var(--surface-1);
    border-top: 1px solid var(--border);
  }

  .metrics {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
    flex-shrink: 0;
  }

  .metric.estimate {
    color: var(--text-secondary);
  }

  .sep {
    color: var(--border);
  }

  .tier {
    text-transform: uppercase;
    letter-spacing: 0.02em;
  }

  .pip {
    display: inline-block;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
    animation: pulse 1.1s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 0.25; }
    50% { opacity: 1; }
  }

  .loading {
    font-style: italic;
  }

  .warnings {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .warning {
    color: var(--status-warning);
  }
</style>
