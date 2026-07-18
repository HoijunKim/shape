<script lang="ts">
  // The explorer shell (T8): the ONLY view after a file opens (spec's
  // product decision -- the P3 dashboard components stay in the repo but are
  // no longer routed to). Reads the `explorer` store directly (no props) and
  // renders every state the store can be in, so a caller (App.svelte) only
  // ever needs `<Explorer on:open={...} />`.
  import { explorer } from "./store";
  import DataTable from "./DataTable.svelte";
  import StructureMap from "./StructureMap.svelte";
  import StatusBar from "./StatusBar.svelte";
  import FileDrop from "../FileDrop.svelte";

  // Obligation 1 (carried from earlier reviews): StructureMap deliberately
  // does not own columnPaths -- it must be built here, from $explorer.columns
  // alone, and kept fresh. This is a plain reactive statement (not memoized
  // outside of Svelte's own dependency tracking) so it is rebuilt as a FRESH
  // Set every time $explorer.columns changes identity, in particular across
  // a close()/open() pair when a second file is opened -- a Set computed
  // once and reused would keep dimming/un-dimming rows by the PREVIOUS
  // file's column set.
  $: columnPaths = new Set($explorer.columns.map((c) => c.path));

  // Obligation 2 (carried from earlier reviews): focusToken must be bumped
  // UNCONDITIONALLY on every focus event, from either StructureMap (a sidebar
  // click) or DataTable (a header click) -- never conditionally on "is this
  // a repeat of the current focusPath". Svelte no-ops a same-value prop
  // re-assignment, so re-dispatching an already-current focusPath through
  // `explorer.focus()` alone would never re-trigger StructureMap/TreeNode's
  // re-expand check, and a branch the user manually collapsed after the
  // first focus would stay collapsed forever on a second click of the same
  // header/row.
  let focusToken = 0;
  function onFocus(e: CustomEvent<{ path: string }>): void {
    explorer.focus(e.detail.path);
    focusToken += 1;
  }

  function retry(): void {
    void explorer.open($explorer.path);
  }

  $: fileName = $explorer.path ? $explorer.path.replace(/^.*[\\/]/, "") : "";
</script>

{#if $explorer.status === "idle"}
  <FileDrop on:open />
{:else if $explorer.status === "opening"}
  <div class="skeleton-shell" aria-busy="true" aria-label="Opening {fileName}">
    <p class="skeleton-label">Opening {fileName}…</p>
    <div class="skeleton-table">
      <div class="skeleton-row header">
        {#each Array(6) as _, i (i)}
          <span class="skeleton-cell"></span>
        {/each}
      </div>
      {#each Array(10) as _, i (i)}
        <div class="skeleton-row" class:odd={i % 2 === 1}>
          {#each Array(6) as _, j (j)}
            <span class="skeleton-cell"></span>
          {/each}
        </div>
      {/each}
    </div>
  </div>
{:else if $explorer.status === "error"}
  <div class="error-shell">
    <p class="error" role="alert">{$explorer.error}</p>
    <button class="primary" on:click={retry}>Retry</button>
  </div>
{:else}
  <!-- ready -->
  <div class="explorer">
    <div class="content">
      <div class="sidebar">
        <StructureMap
          fields={$explorer.fields}
          {columnPaths}
          focusPath={$explorer.focusPath}
          {focusToken}
          on:focus={onFocus}
        />
      </div>
      <div class="main">
        {#if $explorer.columns.length === 0}
          <div class="empty-state">
            <p>No columns detected</p>
            {#if $explorer.skipped > 0}
              <p class="hint">{$explorer.skipped.toLocaleString()} rows skipped</p>
            {/if}
          </div>
        {:else if $explorer.total === 0 && ($explorer.totalExact || $explorer.version > 0)}
          <div class="empty-state">
            <p>No rows in this file</p>
            <p class="hint">
              {$explorer.columns.length.toLocaleString()}
              column{$explorer.columns.length === 1 ? "" : "s"}
            </p>
          </div>
        {:else}
          <!-- Obligation 3 (carried from earlier reviews): DataTable's
               `columns` prop must be $explorer.columns DIRECTLY, never a
               filtered/derived subset -- DataTable reaches back into the
               explorer singleton for rowAt/version/ensurePages, whose page
               arithmetic is keyed off $explorer.columns.length. Passing
               anything else desyncs the fetch window from the rendered
               grid. -->
          <DataTable
            columns={$explorer.columns}
            total={$explorer.total}
            focusPath={$explorer.focusPath}
            on:focus={onFocus}
          />
        {/if}
      </div>
    </div>
    <StatusBar
      tier={$explorer.tier}
      total={$explorer.total}
      totalExact={$explorer.totalExact}
      sampled={$explorer.sampled}
      rowsLoaded={$explorer.version > 0}
      columnCount={$explorer.columns.length}
      columnsTruncated={$explorer.columnsTruncated}
      totalPaths={$explorer.totalPaths}
      warnings={$explorer.warnings}
      fetching={$explorer.fetching}
    />
  </div>
{/if}

<style>
  .skeleton-shell {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    padding: var(--space-4);
    box-sizing: border-box;
    gap: var(--space-3);
  }

  .skeleton-label {
    margin: 0;
    font-size: 13px;
    color: var(--text-muted);
  }

  .skeleton-table {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    overflow: hidden;
    background: var(--surface-1);
  }

  .skeleton-row {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: 0 var(--space-3);
    height: 28px;
    flex-shrink: 0;
    border-bottom: 1px solid var(--border);
  }

  .skeleton-row.header {
    height: 32px;
    background: var(--surface-2);
  }

  .skeleton-row.odd {
    background: color-mix(in srgb, var(--text-muted) 5%, var(--surface-1));
  }

  .skeleton-cell {
    display: block;
    flex: 1;
    height: 12px;
    max-width: 140px;
    border-radius: 3px;
    background: color-mix(in srgb, var(--text-muted) 18%, transparent);
    animation: skeleton-shimmer 1.4s ease-in-out infinite;
  }

  @keyframes skeleton-shimmer {
    0%, 100% { opacity: 0.6; }
    50% { opacity: 1; }
  }

  .error-shell {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-3);
    padding: var(--space-5);
  }

  .error-shell .error {
    margin: 0;
    padding: var(--space-2) var(--space-4);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    font-size: 13px;
    border-radius: var(--radius-sm);
    max-width: 60ch;
    text-align: center;
  }

  .explorer {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    min-width: 0;
  }

  .content {
    flex: 1;
    display: flex;
    min-height: 0;
    min-width: 0;
  }

  .sidebar {
    flex: 0 0 300px;
    min-width: 0;
    min-height: 0;
    border-right: 1px solid var(--border);
  }

  .main {
    flex: 1 1 0%;
    min-width: 0;
    min-height: 0;
    position: relative;
  }

  .empty-state {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    color: var(--text-muted);
    text-align: center;
  }

  .empty-state p {
    margin: 0;
  }

  .empty-state .hint {
    font-size: 12px;
  }

  /* Below this width the sidebar and the table stack instead of sitting
     side by side, mirroring the breakpoint the pre-explorer dashboard used
     for its own grid/detail split (App.svelte's .detail-pane). Unlike that
     card, the sidebar here is a scrollable tree with no natural height, so
     it gets a capped height when stacked rather than growing to fill the
     column -- otherwise it could push the table fully off-screen on a deep
     tree. */
  @media (max-width: 900px) {
    .content {
      flex-direction: column;
    }

    .sidebar {
      flex: 0 0 auto;
      max-height: 240px;
      width: 100%;
      border-right: none;
      border-bottom: 1px solid var(--border);
    }

    .main {
      flex: 1 1 auto;
      width: 100%;
    }
  }
</style>
