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
  import FilterBar from "./FilterBar.svelte";
  import TransformPanel from "./TransformPanel.svelte";
  import ExportDialog from "./ExportDialog.svelte";
  import CodegenPanel from "./CodegenPanel.svelte";
  import SearchBar from "./SearchBar.svelte";
  import ValueTreeOverlay from "./ValueTreeOverlay.svelte";
  import EditBar from "./EditBar.svelte";
  import EditedRows from "./EditedRows.svelte";
  import SaveDialog from "./SaveDialog.svelte";

  // E3 Task 7: a BINDABLE prop, not a local `let` -- Header/Explorer are
  // siblings under App.svelte (Explorer never mounts Header), so App owns
  // the toggle wiring and passes this down; Task 9's seed sets it from
  // outside and it must propagate back up through App's `bind:filterOpen` to
  // Header's aria-pressed.
  export let filterOpen = false;
  // E4: same ownership as filterOpen -- App holds them, Explorer mounts the
  // panels. exportOpen is written back here (the dialog closes itself), which
  // is why both are bindable props rather than plain locals.
  export let columnsOpen = false;
  export let exportOpen = false;
  export let codeOpen = false;

  // E4: the columns panel owns the draft, the export dialog needs to know when
  // that draft is invalid, and the two are siblings -- so Explorer routes it,
  // exactly as it routes the sidebar's seedFilter event below. Un-debounced on
  // the panel's side, so the Export button can never lag the panel's own
  // inline message.
  let transformErrors: string[] = [];

  // Obligation 1 (carried from earlier reviews): StructureMap deliberately
  // does not own columnPaths -- it must be built here, from $explorer.columns
  // alone, and kept fresh. This is a plain reactive statement (not memoized
  // outside of Svelte's own dependency tracking) so it is rebuilt as a FRESH
  // Set every time $explorer.columns changes identity, in particular across
  // a close()/open() pair when a second file is opened -- a Set computed
  // once and reused would keep dimming/un-dimming rows by the PREVIOUS
  // file's column set.
  // E4: the structure map describes the SOURCE, so it is joined to the base
  // column set -- under a projection, $explorer.columns holds output names
  // (possibly renamed), which would dim every row in the sidebar.
  $: columnPaths = new Set($explorer.baseColumns.map((c) => c.path));

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

  // E3 Task 9 (click-to-seed): Explorer is the router between the sidebar's
  // seedFilter event and FilterBar -- opening the bar via the bindable
  // filterOpen prop (Task 7's whole reason for making it bindable rather than
  // a local `let`) and handing the seed down as a nonce-tagged object so
  // FilterBar can tell a fresh seed apart from an unrelated re-render (see
  // FilterBar.svelte's own prevSeedNonce guard comment).
  let seed: { path: string; type: string; nonce: number } | null = null;
  let seedNonce = 0;
  function onSeedFilter(e: CustomEvent<{ path: string; type: string }>): void {
    filterOpen = true;
    seed = { path: e.detail.path, type: e.detail.type, nonce: seedNonce++ };
  }

  function retry(): void {
    void explorer.open($explorer.path);
  }

  // E6 Task 7: the cell value-tree overlay. Explorer owns the fetch + the
  // overlay's open/loading/error/value state; DataTable only dispatches which
  // cell was clicked. A concurrency guard (cellReq) keeps a slow fetch for cell
  // A from landing into the overlay after the user has clicked cell B.
  let cellOpen = false;
  let cellLoading = false;
  let cellError = "";
  let cellValue: unknown = null;
  let cellFound = true;
  let cellLabel = "";
  let cellReq = 0;

  async function onExpandCell(e: CustomEvent<{ index: number; path: string }>): Promise<void> {
    const { index, path } = e.detail;
    const myReq = ++cellReq;
    cellOpen = true;
    cellLoading = true;
    cellError = "";
    cellLabel = path;
    try {
      const res = await explorer.getCell(index, path);
      if (myReq !== cellReq) return; // superseded by a newer expand click
      cellValue = res.value;
      cellFound = res.found;
      cellLoading = false;
    } catch (err) {
      if (myReq !== cellReq) return;
      cellError = String(err);
      cellLoading = false;
    }
  }

  // E10: the row detail view -- the whole record in the SAME overlay as the cell
  // tree, fetched via getCell with the ROOT path ("") which returns the entire
  // record. Shares cellReq, so a slow row fetch cannot land after the user opens
  // a different row or cell.
  async function onExpandRow(e: CustomEvent<{ index: number }>): Promise<void> {
    const { index } = e.detail;
    const myReq = ++cellReq;
    cellOpen = true;
    cellLoading = true;
    cellError = "";
    cellLabel = `Row ${index}`;
    try {
      const res = await explorer.getCell(index, ""); // "" = the whole record
      if (myReq !== cellReq) return;
      cellValue = res.value;
      cellFound = res.found;
      cellLoading = false;
    } catch (err) {
      if (myReq !== cellReq) return;
      cellError = String(err);
      cellLoading = false;
    }
  }

  function closeCell(): void {
    cellReq++; // any in-flight fetch must not reopen the overlay after this
    cellOpen = false;
  }

  // E3 Task 8: the empty state's "Clear filter" affordance -- distinct from
  // StatusBar's Cancel (which only stops an in-flight CountMatches via
  // explorer.cancelCount()). This resets the filter to match-all so the
  // unfiltered rows return, AND bumps filterClearNonce so FilterBar (which owns
  // its own draft, independent of $explorer) resets its condition rows too --
  // otherwise the bar keeps showing the just-cleared conditions over genuinely
  // unfiltered data (the E3 known gap, now closed).
  let filterClearNonce = 0;
  function clearFilter(): void {
    explorer.setFilter({ combinator: "and" } as any);
    filterClearNonce += 1;
  }

  // E7: the edit toolbar's "Edited only" toggle and the save dialog's open
  // state. editedOnly is guarded at the render site by editedCount > 0, so
  // reverting the last edit (here or in the diff view) falls back to the table
  // rather than stranding the user on an empty "No edits yet" pane with no
  // toolbar left to toggle back.
  let editedOnly = false;
  let saveOpen = false;

  // Drop out of the edited-only view the moment the overlay empties -- whether
  // by Revert all, reverting the last cell in the diff view, or opening a new
  // file (open() resets editedCount but cannot touch this component-local). The
  // render guard already hides the empty diff view, but without this the flag
  // stays true and silently re-arms the view on the NEXT edit the user makes.
  $: if ($explorer.editedCount === 0) editedOnly = false;

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
          on:seedFilter={onSeedFilter}
        />
      </div>
      <div class="main">
        <!-- E6: the global search box is ALWAYS visible here (never gated
             behind the filter panel's `{#if open}`), so a search is reachable
             the moment a file is open. -->
        <SearchBar />
        <!-- E7: the edit toolbar sits between the search box and the grid. It
             renders itself only when the overlay is non-empty; the `save` event
             opens the copy dialog (parent-owned, like Export). -->
        <EditBar bind:editedOnly on:save={() => (saveOpen = true)} />
        <!-- A5: a MID-SCROLL page-fetch failure must be non-destructive -- it
             must not discard an already-rendered grid or the user's scroll
             position (unlike `status === "error"` above, which owns the
             whole pane and is reserved for an open()-time failure). This bar
             sits above whichever branch below is showing (almost always the
             DataTable one, since a page fetch only ever happens once the
             file is open); dismissing it never touches the store's real
             data, and Retry re-requests the same row range that failed. -->
        {#if $explorer.pageError}
          <div class="page-error-bar" role="alert">
            <span class="msg">{$explorer.pageError}</span>
            <button class="retry" on:click={() => explorer.retryPageError()}>Retry</button>
            <button class="dismiss" on:click={() => explorer.dismissPageError()} aria-label="Dismiss">✕</button>
          </div>
        {/if}
        {#if editedOnly && $explorer.editedCount > 0}
          <!-- E7: the edited-only diff view replaces the grid. Guarded by
               editedCount so reverting the last edit auto-returns to the
               table (the toolbar toggle is gone at that point). -->
          <EditedRows />
        {:else if $explorer.columns.length === 0}
          <div class="empty-state">
            <p>No columns detected</p>
            {#if $explorer.skipped > 0}
              <p class="hint">{$explorer.skipped.toLocaleString()} rows skipped</p>
            {/if}
          </div>
        {:else if $explorer.total === 0 && ($explorer.totalExact || $explorer.version > 0)}
          <!-- E6 §8 + review #7: an empty result is one of four distinct cases,
               checked in order so filter+search-both-active never mislabels the
               cause. A Clear-filter button appears only where clearing the
               filter is a plausible remedy (it is not when a live search is the
               reason, so the search-only case relies on the box's own ✕). -->
          {#if $explorer.filterActive && $explorer.search !== ""}
            <div class="empty-state">
              <p>No rows match your filter and search</p>
              <p class="hint">no field contains “{$explorer.search}” among the filtered rows</p>
              <button type="button" class="clear-filter" on:click={clearFilter}>Clear filter</button>
            </div>
          {:else if $explorer.filterActive}
            <div class="empty-state">
              <p>No rows match this filter</p>
              <p class="hint">
                {$explorer.columns.length.toLocaleString()}
                column{$explorer.columns.length === 1 ? "" : "s"}
              </p>
              <button type="button" class="clear-filter" on:click={clearFilter}>Clear filter</button>
            </div>
          {:else if $explorer.search !== ""}
            <div class="empty-state">
              <p>No rows match your search</p>
              <p class="hint">no field contains “{$explorer.search}”</p>
            </div>
          {:else}
            <div class="empty-state">
              <p>No rows in this file</p>
              <p class="hint">
                {$explorer.columns.length.toLocaleString()}
                column{$explorer.columns.length === 1 ? "" : "s"}
              </p>
            </div>
          {/if}
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
            resetToken={$explorer.resetToken}
            on:focus={onFocus}
            on:expandCell={onExpandCell}
            on:expandRow={onExpandRow}
          />
        {/if}
      </div>
    </div>
    {#if $explorer.status === "ready"}
      <!-- All three docked panels share ONE bounded, scrollable strip: at
           45vh + 45vh + 40vh they would otherwise stack past the viewport and
           push the status bar off-screen (branch-review M8). The strip caps at
           half the height and scrolls; each panel keeps its own styling.
           E4: a FILTER runs on the record, before projection, so its column
           list is the BASE set -- hiding a column in the transform panel must
           not remove it from the filter's vocabulary. -->
      <div class="panels">
        <FilterBar columns={$explorer.baseColumns} open={filterOpen} {seed} clearNonce={filterClearNonce} />
        <TransformPanel
          columns={$explorer.baseColumns}
          open={columnsOpen}
          on:errors={(e) => (transformErrors = e.detail)}
        />
        <CodegenPanel open={codeOpen} />
      </div>
    {/if}
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
      filterActive={$explorer.filterActive}
      searchActive={$explorer.search !== ""}
      counting={$explorer.counting}
      matchCount={$explorer.matchCount}
      matchExact={$explorer.matchExact}
      transformActive={$explorer.transformActive}
      baseColumnCount={$explorer.baseColumns.length}
      on:cancelCount={() => explorer.cancelCount()}
    />
    <ExportDialog
      open={exportOpen}
      disabledReason={transformErrors[0] ?? ""}
      on:close={() => (exportOpen = false)}
    />
    <SaveDialog open={saveOpen} on:close={() => (saveOpen = false)} />
    <ValueTreeOverlay
      open={cellOpen}
      loading={cellLoading}
      error={cellError}
      value={cellValue}
      found={cellFound}
      label={cellLabel}
      on:close={closeCell}
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

  /* One scrollable strip for the docked panels, capped so the table and
     the status bar always stay visible however many panels are open. */
  .panels {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    max-height: 50vh;
    overflow-y: auto;
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

  .page-error-bar {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    z-index: 5; /* above DataTable's sticky header (z-index 3) and corner (4) */
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    font-size: 12px;
    border-bottom: 1px solid var(--status-critical);
  }

  .page-error-bar .msg {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .page-error-bar button {
    flex-shrink: 0;
    padding: 2px var(--space-2);
    font-size: 12px;
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

  .empty-state .clear-filter {
    margin-top: var(--space-2);
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
