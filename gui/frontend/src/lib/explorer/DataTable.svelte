<script lang="ts">
  // The virtualized data table: fixed row height, hand-rolled two-axis
  // virtualization (rows AND columns), sticky header + sticky row-index
  // gutter. No library -- see brief for why (zero runtime deps).
  import { createEventDispatcher, onMount } from "svelte";
  import { explorer } from "./store";
  import type { Column, Row } from "./types";
  import { CellKind } from "./types";
  import { columnWidths, prefixSums, columnAt, rowWindow, alignForKind, clamp } from "./widths";
  import CellView from "./CellView.svelte";

  export let columns: Column[] = [];
  export let total = 0;
  export let focusPath = "";
  // E3 Task 9 (recon GAP 9): bumped by the store on every setFilter() call.
  // DataTable is the sole owner of the scroll viewport, so the store cannot
  // reset scroll itself -- it just bumps this counter and this component
  // reacts. See the prevResetToken guard below.
  export let resetToken = 0;

  const dispatch = createEventDispatcher<{
    focus: { path: string };
    // E6 Task 7: a click on a container (object/array) cell's expand affordance
    // asks Explorer to fetch and show the cell's FULL value. index is the
    // absolute Row.Index the table already rendered; path is the column path.
    expandCell: { index: number; path: string };
  }>();

  // Only object/array cells get an expand affordance -- a scalar's full value
  // is already shown in the cell. Mirrors CellView's container branches.
  function isContainerCell(kind: CellKind): boolean {
    return kind === CellKind.OBJECT || kind === CellKind.ARRAY;
  }

  const ROW_H = 28;
  const HEADER_H = 32;
  const GUTTER_W = 64;
  const OVERSCAN_ROWS = 8;
  const OVERSCAN_COLS = 3;

  let viewportEl: HTMLDivElement;

  // Visible window, recomputed by recomputeRange() on scroll, mount, resize,
  // a `columns` identity change, or `total` changing (see the `safeTotal`
  // reactive trigger below -- finding (a)).
  let firstRow = 0;
  let lastRow = -1;
  let firstCol = 0;
  let lastCol = -1;

  $: safeTotal = Math.max(0, total);
  $: widths = columnWidths(columns);
  $: prefix = prefixSums(widths);
  $: totalWidth = prefix.length ? prefix[prefix.length - 1] : 0;
  $: contentWidth = GUTTER_W + totalWidth;
  $: contentHeight = HEADER_H + safeTotal * ROW_H;

  // columnAt/rowWindow (the row/column window math) and alignForKind live in
  // widths.ts, not here -- vitest can't reach pure functions buried in a
  // .svelte file, and this is the highest boundary-risk arithmetic in the
  // table (binary search edges, total <= 0, a shrinking total, 512 columns).

  // Tracks the range last actually requested from the store, so a scroll
  // tick that doesn't change the visible row window (e.g. pure horizontal
  // scroll on a wide table) doesn't re-call ensurePages() -- finding (b):
  // ensurePages() unconditionally writes a fresh object to the store even
  // in the all-cached case (to flip `fetching`), and Svelte's
  // safe_not_equal invalidates on that object identity alone, which would
  // otherwise force every visible CellView to re-run its reactive
  // statements on every rAF tick regardless of whether the row window
  // actually moved.
  let lastRequestedFirst = -1;
  let lastRequestedLast = -2; // sentinel below lastRequestedFirst: the first real range always fires

  function recomputeRange(): void {
    if (!viewportEl) return;
    const scrollTop = viewportEl.scrollTop;
    const scrollLeft = viewportEl.scrollLeft;
    const clientHeight = viewportEl.clientHeight;
    const clientWidth = viewportEl.clientWidth;

    const win = rowWindow(scrollTop, clientHeight, safeTotal, ROW_H, OVERSCAN_ROWS);
    firstRow = win.firstRow;
    lastRow = win.lastRow;

    if (columns.length === 0) {
      firstCol = 0;
      lastCol = -1;
    } else {
      const maxCol = columns.length - 1;
      // scrollLeft/scrollTop are used directly here (not offset by
      // GUTTER_W/HEADER_H) even though column c's content starts at
      // GUTTER_W + prefix[c] and row content starts at HEADER_H + i*ROW_H.
      // That's intentional, not a missed offset: the sticky gutter/header
      // occlude exactly GUTTER_W/HEADER_H of content at the leading edge,
      // and that occlusion offset exactly cancels the content's own
      // GUTTER_W/HEADER_H starting offset -- e.g. at scrollTop=S the first
      // row NOT hidden behind the sticky header sits at content-y
      // S + HEADER_H, which is row index (S + HEADER_H - HEADER_H) / ROW_H
      // = S / ROW_H, exactly today's formula. Concretely, at ROW_H=28,
      // HEADER_H=32, scrollTop=56: row 2 (content-y [88,116)) is the first
      // row below the header (header covers content-y [56,88)), and
      // floor(56/28) = 2 -- matches with no adjustment. So this is not
      // "absorbed by overscan", it's exact; OVERSCAN_ROWS/OVERSCAN_COLS
      // give genuine extra margin beyond the boundary, not compensation
      // for a missing correction. (Verified against the symmetric column
      // case too, via GUTTER_W the same way.)
      //
      // NOTE this exactness is a LEADING-edge property only. The trailing
      // edge is deliberately over-inclusive: ceil((S + clientHeight)/ROW_H)
      // overshoots the true last visible row by about HEADER_H/ROW_H + 1
      // (~2.1 rows), and columnAt(scrollLeft + clientWidth) is over-inclusive
      // by up to GUTTER_W (~1 column). Both over-render, never under-render.
      // So the trailing edge already carries ~2-3 rows of implicit slack on
      // top of OVERSCAN_ROWS -- do not trim the overscan constants believing
      // the trailing figure is tight.
      firstCol = clamp(columnAt(scrollLeft, prefix) - OVERSCAN_COLS, 0, maxCol);
      lastCol = clamp(columnAt(scrollLeft + clientWidth, prefix) + OVERSCAN_COLS, 0, maxCol);
    }

    if (lastRow < firstRow) {
      // Empty window (total went to 0). Forget the memo: if total grows back
      // to the same range later, the request must fire again rather than be
      // suppressed as a duplicate.
      lastRequestedFirst = -1;
      lastRequestedLast = -2;
    } else if (firstRow !== lastRequestedFirst || lastRow !== lastRequestedLast) {
      lastRequestedFirst = firstRow;
      lastRequestedLast = lastRow;
      void explorer.ensurePages(firstRow, lastRow);
    }
  }

  let rafId = 0;
  function onScroll(): void {
    if (rafId) return;
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      recomputeRange();
    });
  }

  // Finding (a): `total` can SHRINK after mount -- paging.ts's reconcileEof
  // optimistically projects `total = pageEnd + pageRows` on the rescan tier
  // (totalExact === false) and corrects it down once the true EOF page
  // lands. firstRow/lastRow were otherwise only recomputed on scroll,
  // mount, resize, or a `columns` identity change -- never on a bare
  // `total` change -- so without this trigger a shrink left lastRow
  // pointing past the new end: rows [newTotal, lastRow] kept rendering as
  // skeletons that could never resolve (no such row/page exists to land).
  // recomputeRange() re-reads the live scrollTop/clientHeight itself, so no
  // scroll event is needed for this to take effect.
  $: safeTotal, recomputeRange();

  // A new file opened (columns is a fresh array reference from store.open())
  // -- reset scroll to the origin and recompute against the new shape.
  let prevColumns: Column[] | null = null;
  $: if (columns !== prevColumns) {
    prevColumns = columns;
    // This block's own re-run trigger is textually `columns` alone;
    // recomputeRange() below reads `widths`/`prefix` (via columnAt), which
    // are recomputed from `columns` by the reactive statements above. That
    // ordering held before only because of source position -- reference
    // them directly so Svelte's dependency graph enforces it explicitly,
    // not by accident of where this block sits in the file (finding (e)).
    void widths;
    void prefix;
    // A new file also invalidates any previously-requested range: reset it
    // so the fetch for row 0 of the NEW file isn't skipped merely because
    // it happens to numerically match the OLD file's last-requested range.
    lastRequestedFirst = -1;
    lastRequestedLast = -2;
    if (viewportEl) {
      viewportEl.scrollTop = 0;
      viewportEl.scrollLeft = 0;
    }
    recomputeRange();
  }

  function scrollToTop(): void {
    if (!viewportEl) return;
    viewportEl.scrollTop = 0;
    recomputeRange();
  }

  // E3 Task 9: react to the store's resetToken (recon GAP 9 -- a filter
  // change should return the user to row 0, since the previously-scrolled-to
  // rows may no longer exist/match). Guarded by a prevResetToken sentinel
  // (mirrors the `columns`-changed guard above) initialized to the prop's OWN
  // incoming value -- not e.g. null -- so mounting (or a later prop re-pass
  // that happens to carry the same value) never forces a scroll on its own;
  // only an actual bump (a real setFilter() call) does. Positioned BEFORE
  // `visibleRows` below for the exact same reason as the `safeTotal` trigger
  // and the `columns` guard above (see visibleRows's own "Finding (a)
  // backstop" comment): recomputeRange() reassigns firstRow/lastRow via a
  // plain (non-`$:`) mutation, so visibleRows only picks up the fresh values
  // if this block's own reassignment runs BEFORE visibleRows executes in the
  // same synchronous update pass -- placed after it, the row window would
  // silently keep rendering the pre-reset (deep-scrolled) rows forever, since
  // Svelte does not re-run an earlier `$:` statement mid-pass just because a
  // later one mutated one of its reads.
  let prevResetToken = resetToken;
  $: if (resetToken !== prevResetToken) {
    prevResetToken = resetToken;
    scrollToTop();
  }

  onMount(() => {
    recomputeRange();
    const onResize = () => recomputeRange();
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      if (rafId) cancelAnimationFrame(rafId);
    };
  });

  // Reactivity trigger (M5/T5): the store's page cache is invisible to
  // Svelte, so `version` must be read in the same reactive statement that
  // calls rowAt(), or a landed page never repaints.
  $: visibleRows = (() => {
    void $explorer.version;
    // Finding (a) backstop. The safeTotal trigger above is the actual fix;
    // this clamp exists because THIS statement has no declared dependency
    // edge to it -- firstRow/lastRow are assigned inside recomputeRange(), so
    // Svelte's topological sorter cannot see them, and the two statements are
    // ordered only by source position. Move this block above that trigger and
    // it would render one stale frame; the clamp is what contains that.
    // Never iterate past the current total.
    const effectiveFirstRow = clamp(firstRow, 0, Math.max(0, safeTotal - 1));
    const effectiveLastRow = Math.min(lastRow, safeTotal - 1);
    const out: { i: number; row: Row | null }[] = [];
    for (let i = effectiveFirstRow; i <= effectiveLastRow; i++) {
      out.push({ i, row: explorer.rowAt(i).row });
    }
    return out;
  })();

  $: visibleCols = (() => {
    const out: { c: number; col: Column }[] = [];
    for (let c = firstCol; c <= lastCol; c++) {
      out.push({ c, col: columns[c] });
    }
    return out;
  })();

  // A header click is a focus, not a sort (there is no sorting in E2). The
  // click already scrolled the user to this column, so suppress the
  // "scroll the focused column into view" reactive block below for the
  // resulting prop echo -- only an externally-driven focus change (e.g. a
  // future sidebar selection) should force a scroll.
  let suppressNextScroll = false;
  function onHeaderClick(path: string): void {
    suppressNextScroll = true;
    dispatch("focus", { path });
  }

  function scrollToColumn(path: string): void {
    if (!viewportEl) return;
    const idx = columns.findIndex((c) => c.path === path);
    if (idx < 0) return;
    viewportEl.scrollLeft = Math.max(0, prefix[idx] - 24);
    recomputeRange();
  }

  let prevFocusPath = focusPath;
  $: if (focusPath !== prevFocusPath) {
    prevFocusPath = focusPath;
    if (suppressNextScroll) {
      suppressNextScroll = false;
    } else {
      scrollToColumn(focusPath);
    }
  }
</script>

<div
  class="viewport"
  bind:this={viewportEl}
  on:scroll={onScroll}
  role="grid"
  aria-rowcount={safeTotal}
  aria-colcount={columns.length}
>
  <div class="content" style="width:{contentWidth}px; height:{contentHeight}px;">
    <div class="header" style="height:{HEADER_H}px;">
      <div class="corner" style="width:{GUTTER_W}px; height:{HEADER_H}px;"></div>
      {#each visibleCols as { c, col } (col.path)}
        <button
          type="button"
          class="header-cell"
          class:focused={col.path === focusPath}
          style="left:{GUTTER_W + prefix[c]}px; width:{widths[c]}px; height:{HEADER_H}px;"
          title={col.path}
          on:click={() => onHeaderClick(col.path)}
        >
          {col.name}
        </button>
      {/each}
    </div>

    <div class="rows" style="top:{HEADER_H}px;">
      {#each visibleRows as { i, row } (i)}
        <div
          class="row"
          class:odd={i % 2 === 1}
          role="row"
          style="top:{i * ROW_H}px; height:{ROW_H}px; width:{contentWidth}px;"
        >
          <div class="gutter-cell" role="rowheader" style="width:{GUTTER_W}px;">
            {#if row}
              {row.index}
            {:else}
              <span class="skeleton-bar gutter-skel"></span>
            {/if}
          </div>
          {#each visibleCols as { c, col } (col.path)}
            <div
              class="data-cell"
              role="gridcell"
              style="left:{GUTTER_W + prefix[c]}px; width:{widths[c]}px; height:{ROW_H}px;"
            >
              {#if row && row.cells[c]}
                <CellView cell={row.cells[c]} align={alignForKind(row.cells[c].kind)} />
                {#if isContainerCell(row.cells[c].kind)}
                  <button
                    type="button"
                    class="expand-btn"
                    aria-label="Expand value"
                    title="Expand value"
                    on:click={() => dispatch("expandCell", { index: row.index, path: col.path })}
                  >⤢</button>
                {/if}
              {:else if row}
                <!-- Row landed but this column has no cell. That is a real
                     absence, not a pending fetch, so it must NOT render as a
                     skeleton (a skeleton promises "will resolve"; this never
                     will). Render it the same way a genuinely missing value
                     is rendered. -->
                <CellView cell={{ kind: CellKind.MISSING }} align="left" />
              {:else}
                <span class="skeleton-bar"></span>
              {/if}
            </div>
          {/each}
        </div>
      {/each}
    </div>
  </div>
</div>

<style>
  .viewport {
    position: relative;
    width: 100%;
    height: 100%;
    overflow: auto;
    background: var(--surface-1);
  }

  .content {
    position: relative;
  }

  .header {
    position: sticky;
    top: 0;
    z-index: 3;
    background: var(--surface-1);
    border-bottom: 1px solid var(--border);
  }

  .corner {
    position: sticky;
    left: 0;
    z-index: 4;
    display: inline-block;
    background: var(--surface-1);
    border-right: 1px solid var(--border);
    box-sizing: border-box;
  }

  .header-cell {
    position: absolute;
    top: 0;
    display: flex;
    align-items: center;
    box-sizing: border-box;
    padding: 0 var(--space-2);
    margin: 0;
    font-size: 12px;
    font-weight: 600;
    text-align: left;
    color: var(--text-secondary);
    background: var(--surface-1);
    border: none;
    border-right: 1px solid var(--border);
    border-radius: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  .header-cell:hover {
    background: var(--surface-2);
    border-color: var(--border);
  }

  .header-cell.focused {
    color: var(--accent);
    background: color-mix(in srgb, var(--accent) 12%, var(--surface-1));
  }

  .rows {
    position: absolute;
    left: 0;
    right: 0;
  }

  .row {
    position: absolute;
    left: 0;
    background: var(--surface-1);
  }

  .row.odd {
    background: color-mix(in srgb, var(--text-muted) 5%, var(--surface-1));
  }

  .gutter-cell {
    position: sticky;
    left: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    height: 100%;
    box-sizing: border-box;
    padding: 0 var(--space-2);
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--surface-1);
    border-right: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
  }

  .data-cell {
    position: absolute;
    top: 0;
    display: flex;
    align-items: center;
    box-sizing: border-box;
    overflow: hidden;
    border-right: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
  }

  /* The expand affordance on a container cell. Quiet by default (opacity, not
     display:none, so it stays discoverable and clickable), lifted on hover of
     its own cell. */
  .expand-btn {
    position: absolute;
    right: 2px;
    top: 50%;
    transform: translateY(-50%);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1;
    opacity: 0;
    cursor: pointer;
  }

  .data-cell:hover .expand-btn,
  .expand-btn:focus-visible {
    opacity: 1;
  }

  .expand-btn:hover {
    color: var(--accent);
    background: color-mix(in srgb, var(--text-muted) 12%, var(--surface-2));
  }

  .expand-btn:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .skeleton-bar {
    display: block;
    width: 70%;
    height: 12px;
    margin: auto 0;
    border-radius: 3px;
    background: color-mix(in srgb, var(--text-muted) 20%, transparent);
  }

  .gutter-skel {
    width: 60%;
    margin-left: auto;
  }
</style>
