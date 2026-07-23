<script lang="ts">
  // The explorer's bottom status bar (T8, spec §4): the sole place the app
  // ever states a row/column count, so it is the sole place the honesty
  // rules (never present an estimate as exact; never reword the streaming-
  // mode warning) get enforced. Every prop here is a plain, already-computed
  // value read straight off $explorer by Explorer.svelte -- this component
  // owns no store subscription of its own.
  import { createEventDispatcher } from "svelte";
  import { formatRowCount } from "./rowCount";

  const dispatch = createEventDispatcher<{ cancelCount: void }>();

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
  // E3 Task 8: filtered-count props (spec §4's honest counting affordance,
  // recon GAP 1/6). `filterActive` alone gates BOTH formatRowCount's filtered
  // branch and the Cancel button below; when it is false this component
  // renders exactly as it did in E2 -- `counting`/`matchCount`/`matchExact`
  // are meaningless (and ignored by formatRowCount) unless it is true.
  export let filterActive = false;
  // E6: a global search runs CountMatches on the non-memory tiers exactly like
  // a filter, so the counting affordance (the "counting…" match-count branch
  // AND the Cancel button) must gate on filter-OR-search active, not the filter
  // alone -- otherwise a search-only count shows "counting…" forever with no
  // way to cancel it.
  export let searchActive = false;
  export let counting = false;
  export let matchCount = -1;
  export let matchExact = false;
  // E4 Task 11: a column PROJECTION is a different thing from the wide-data
  // cap, and needs its own denominator. Under any Transform.Select the engine
  // sets totalPaths = len(RowSet.Columns) and columnsTruncated = false
  // (engine.go's QueryRows), so a 3-of-12 projection would otherwise read
  // "3 of 3". baseColumnCount is the source's own column count, straight from
  // $explorer.baseColumns.
  export let transformActive = false;
  export let baseColumnCount = 0;

  // A filtered OR searched view both route through CountMatches and set
  // matchCount, so formatRowCount's "a match count applies" branch keys on both.
  $: countActive = filterActive || searchActive;
  $: rowsText = formatRowCount({
    total, totalExact, rowsLoaded, filterActive: countActive, counting, matchCount, matchExact,
  });
  $: columnsText = transformActive
    ? `showing ${columnCount.toLocaleString()} of ${baseColumnCount.toLocaleString()} columns`
    : columnsTruncated
      ? `showing ${columnCount.toLocaleString()} of ${totalPaths.toLocaleString()} columns`
      : `${columnCount.toLocaleString()} column${columnCount === 1 ? "" : "s"}`;

  function onCancelClick(): void {
    dispatch("cancelCount");
  }
</script>

<div class="status-bar">
  <div class="metrics" role="status">
    <span class="metric mono" class:estimate={sampled && !totalExact}>{rowsText}</span>
    {#if countActive && counting}
      <button type="button" class="cancel-count" on:click={onCancelClick}>Cancel</button>
    {/if}
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

  <!-- Obligation carried from earlier reviews: the warnings strings render
       VERBATIM -- in particular the streaming-mode string, which a Go test
       (TestEngine_OpenSource_RescanTier_StreamingWarningExact) matches
       byte-for-byte, so it is never reworded or wrapped in extra punctuation
       here.
       A3: gated on `warnings.length > 0` alone, not `sampled && ...` -- every
       warning that exists today happens to be a rescan-tier (sampled) one,
       but nothing about `warnings` itself is sampled-specific, and gating on
       `sampled` would silently swallow any future non-rescan warning the
       backend adds. -->
  {#if warnings.length > 0}
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

  .cancel-count {
    flex-shrink: 0;
    padding: 1px var(--space-2);
    font-size: 11px;
    line-height: 1.4;
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
