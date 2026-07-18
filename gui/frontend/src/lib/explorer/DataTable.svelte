<script lang="ts">
  // The virtualized data table: fixed row height, hand-rolled two-axis
  // virtualization (rows AND columns), sticky header + sticky row-index
  // gutter. No library -- see brief for why (zero runtime deps).
  import { createEventDispatcher, onMount } from "svelte";
  import { explorer } from "./store";
  import type { Column, Row } from "./types";
  import { CellKind } from "./types";
  import { columnWidths, prefixSums } from "./widths";
  import CellView from "./CellView.svelte";

  export let columns: Column[] = [];
  export let total = 0;
  export let focusPath = "";

  const dispatch = createEventDispatcher<{ focus: { path: string } }>();

  const ROW_H = 28;
  const HEADER_H = 32;
  const GUTTER_W = 64;
  const OVERSCAN_ROWS = 8;
  const OVERSCAN_COLS = 3;

  let viewportEl: HTMLDivElement;

  // Visible window, recomputed by recomputeRange() on scroll/mount/resize.
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

  function clamp(v: number, lo: number, hi: number): number {
    return Math.max(lo, Math.min(hi, v));
  }

  // Cell-kind, not column-type, decides alignment (spec §3's table is keyed
  // on kind: a `null` inside an otherwise-numeric column still renders
  // left-aligned, so this can't be precomputed once per column).
  function alignForKind(kind: CellKind): "left" | "right" {
    return kind === CellKind.INT || kind === CellKind.FLOAT ? "right" : "left";
  }

  // Binary search over the prefix-sum array for the column whose span
  // contains x: the greatest i such that prefix[i] <= x.
  function columnAt(x: number): number {
    const n = widths.length;
    if (n === 0) return 0;
    let lo = 0;
    let hi = n - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (prefix[mid] <= x) lo = mid; else hi = mid - 1;
    }
    return lo;
  }

  function recomputeRange(): void {
    if (!viewportEl) return;
    const scrollTop = viewportEl.scrollTop;
    const scrollLeft = viewportEl.scrollLeft;
    const clientHeight = viewportEl.clientHeight;
    const clientWidth = viewportEl.clientWidth;

    if (safeTotal <= 0) {
      firstRow = 0;
      lastRow = -1;
    } else {
      const maxRow = safeTotal - 1;
      firstRow = clamp(Math.floor(scrollTop / ROW_H) - OVERSCAN_ROWS, 0, maxRow);
      lastRow = clamp(Math.ceil((scrollTop + clientHeight) / ROW_H) + OVERSCAN_ROWS, 0, maxRow);
    }

    if (columns.length === 0) {
      firstCol = 0;
      lastCol = -1;
    } else {
      const maxCol = columns.length - 1;
      firstCol = clamp(columnAt(scrollLeft) - OVERSCAN_COLS, 0, maxCol);
      lastCol = clamp(columnAt(scrollLeft + clientWidth) + OVERSCAN_COLS, 0, maxCol);
    }

    if (lastRow >= firstRow) void explorer.ensurePages(firstRow, lastRow);
  }

  let rafId = 0;
  function onScroll(): void {
    if (rafId) return;
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      recomputeRange();
    });
  }

  // A new file opened (columns is a fresh array reference from store.open())
  // -- reset scroll to the origin and recompute against the new shape.
  let prevColumns: Column[] | null = null;
  $: if (columns !== prevColumns) {
    prevColumns = columns;
    if (viewportEl) {
      viewportEl.scrollTop = 0;
      viewportEl.scrollLeft = 0;
    }
    recomputeRange();
  }

  onMount(() => {
    recomputeRange();
    const onResize = () => recomputeRange();
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  });

  // Reactivity trigger (M5/T5): the store's page cache is invisible to
  // Svelte, so `version` must be read in the same reactive statement that
  // calls rowAt(), or a landed page never repaints.
  $: visibleRows = (() => {
    void $explorer.version;
    const out: { i: number; row: Row | null }[] = [];
    for (let i = firstRow; i <= lastRow; i++) {
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
              {#if row}
                <CellView cell={row.cells[c]} align={alignForKind(row.cells[c].kind)} />
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
    box-sizing: border-box;
    overflow: hidden;
    border-right: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
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
