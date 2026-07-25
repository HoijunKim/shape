<script lang="ts">
  // The virtualized data table: fixed row height, hand-rolled two-axis
  // virtualization (rows AND columns), sticky header + sticky row-index
  // gutter. No library -- see brief for why (zero runtime deps).
  import { createEventDispatcher, onMount } from "svelte";
  import { explorer } from "./store";
  import type { Cell, Column, Row } from "./types";
  import { CellKind } from "./types";
  import {
    columnWidths, prefixSums, columnAt, alignForKind, clamp,
    capForDpr, contentHeightFor, isScaled, rowWindowFor, rowTopFor, scrollTopForRow,
  } from "./widths";
  import CellView from "./CellView.svelte";

  export let columns: Column[] = [];
  export let total = 0;
  export let focusPath = "";
  // V1: the DPR-aware scroll-spacer cap. A prop with a devicePixelRatio-derived
  // default (not a bare const) so a test can inject a tiny cap and cross it with
  // a few-hundred-row fixture instead of a 33M-px one. Production never sets it.
  export let maxContentPx: number | undefined = undefined;
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
  // V1: effectiveFirstRow (the clamped top row of the rendered window) is LIFTED
  // to component scope from the visibleRows IIFE, because in scaled mode the
  // template positions rows at (i - effectiveFirstRow)*ROW_H and must see the
  // clamped value -- otherwise a reconcileEof shrink frame renders negative tops.
  let effectiveFirstRow = 0;
  // V1: the current scroll offset and viewport height, tracked in state so the
  // reactive contentHeight/scaled derivations and the scaled window follow them.
  let scrollTop = 0;
  let clientHeight = 0;
  // V1: the display's devicePixelRatio, re-read on resize AND on a
  // matchMedia('(resolution)') change (a monitor move fires no resize).
  let dpr = typeof devicePixelRatio === "number" ? devicePixelRatio : 1;

  $: safeTotal = Math.max(0, total);
  $: widths = columnWidths(columns);
  $: prefix = prefixSums(widths);
  $: totalWidth = prefix.length ? prefix[prefix.length - 1] : 0;
  $: contentWidth = GUTTER_W + totalWidth;
  // V1: the scroll spacer height is capped so Blink never clamps it; past the
  // cap `scaled` switches on the fractional mapping and the native-sticky rows
  // window. maxContentPx (a test seam) overrides the DPR-derived cap.
  $: cap = maxContentPx ?? capForDpr(dpr);
  $: contentHeight = contentHeightFor(safeTotal, ROW_H, HEADER_H, cap);
  $: scaled = isScaled(safeTotal, ROW_H, HEADER_H, cap);
  // Re-derive the window whenever the cap/height/mode changes (e.g. a DPR change
  // that flips scaled), not only on scroll -- review V17.
  $: cap, contentHeight, scaled, recomputeRange();

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
    // Always the browser-CLAMPED scrollTop (read back after any assignment):
    // in scaled mode contentHeight is pinned at the cap, so a requested value
    // could exceed the real max and offset the window (review V4/V6).
    scrollTop = viewportEl.scrollTop;
    const scrollLeft = viewportEl.scrollLeft;
    clientHeight = viewportEl.clientHeight;
    const clientWidth = viewportEl.clientWidth;

    // rowWindowFor delegates to the exact former rowWindow under the cap and
    // uses the header-aware fractional mapping past it.
    const win = rowWindowFor(scrollTop, clientHeight, safeTotal, ROW_H, HEADER_H, OVERSCAN_ROWS, contentHeight);
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
    const onResize = () => { readDpr(); recomputeRange(); };
    window.addEventListener("resize", onResize);
    // V1: a monitor move (different scale) changes devicePixelRatio but does NOT
    // reliably fire resize, so also watch the current resolution and re-arm the
    // watcher after each change. Guarded for jsdom, where matchMedia is absent.
    let mq: MediaQueryList | null = null;
    let onDpr: (() => void) | null = null;
    function armDprWatch(): void {
      if (typeof matchMedia !== "function") return;
      mq = matchMedia(`(resolution: ${dpr}dppx)`);
      onDpr = () => { readDpr(); recomputeRange(); armDprWatch(); };
      mq.addEventListener?.("change", onDpr, { once: true });
    }
    armDprWatch();
    return () => {
      window.removeEventListener("resize", onResize);
      if (mq && onDpr) mq.removeEventListener?.("change", onDpr);
      if (rafId) cancelAnimationFrame(rafId);
    };
  });

  function readDpr(): void {
    if (typeof devicePixelRatio === "number") dpr = devicePixelRatio;
  }

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
    // Never iterate past the current total. effectiveFirstRow is the LIFTED
    // component-level var (not a local const) so the template's rowTopFor sees
    // the SAME clamped value this render was built from -- in scaled mode a row
    // is positioned at (i - effectiveFirstRow)*ROW_H, so a stale/unclamped value
    // would render negative tops on a reconcileEof shrink frame (review V19).
    effectiveFirstRow = clamp(firstRow, 0, Math.max(0, safeTotal - 1));
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

  // A header body click is a focus (scroll-to-column). The click already
  // scrolled the user to this column, so suppress the "scroll the focused
  // column into view" reactive block below for the resulting prop echo -- only
  // an externally-driven focus change (e.g. a sidebar selection) should force a
  // scroll. E9: a click on the sort caret cycles the sort instead (delegated by
  // target, the TreeNode pattern), so the two affordances never collide.
  let suppressNextScroll = false;
  function onHeaderClick(e: MouseEvent, path: string): void {
    if ((e.target as HTMLElement).closest(".sort-caret")) {
      cycleSort(path);
      return;
    }
    suppressNextScroll = true;
    dispatch("focus", { path });
  }

  // E9: cycle a column's sort none -> asc -> desc -> none (a different column
  // starts at asc). Reads the active sort off the store.
  function cycleSort(path: string): void {
    const s = $explorer.sort;
    if (!s || s.path !== path) {
      explorer.setSort({ path, desc: false } as any);
    } else if (!s.desc) {
      explorer.setSort({ path, desc: true } as any);
    } else {
      explorer.setSort({ path: "", desc: false } as any);
    }
  }

  // The ▲/▼ indicator glyph for a column, or "" when it is not the sort column.
  function sortGlyph(path: string): string {
    const s = $explorer.sort;
    if (!s || s.path !== path) return "";
    return s.desc ? "▼" : "▲";
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

  // V1: go-to-row -- the exact-navigation path when scaled drag is coarse (and a
  // convenience when it is not). The input is 1-based; it clamps to [1,total]
  // and scrolls so that row is at the top of the band, then recomputeRange reads
  // the browser-clamped scrollTop back. Exact against the CURRENT total (which,
  // on the rescan tier, is an estimate reconcileEof refines as pages land -- the
  // same live total drag-scroll follows).
  let gotoValue: string | number = "";
  function goToRow(): void {
    if (!viewportEl || safeTotal <= 0) return;
    // An empty box is a no-op, NOT row 0: a number input binds "" to null and
    // Number(null)===0 would pass a NaN guard and yank the user to the top. Only
    // an actually-entered number navigates.
    if (gotoValue == null || String(gotoValue).trim() === "") return;
    const parsed = Math.floor(Number(gotoValue));
    if (!Number.isFinite(parsed)) return;
    const row = clamp(parsed, 1, safeTotal) - 1; // 1-based -> 0-based, clamped
    // Read clientHeight LIVE (not the state var, which lags until the next
    // recompute) so the scroll target matches what recomputeRange will map back.
    const ch = viewportEl.clientHeight;
    viewportEl.scrollTop = scrollTopForRow(row, ch, safeTotal, ROW_H, HEADER_H, contentHeight);
    recomputeRange();
  }
  function onGotoKey(e: KeyboardEvent): void {
    if (e.key === "Enter") { e.preventDefault(); goToRow(); }
  }

  // --- E7: inline scalar editing ------------------------------------------
  // A column is editable only when its source path resolves unambiguously: a
  // single, non-Elem leaf with a unique, non-empty display path (review F2 --
  // an Elem/"[]" or duplicate-path column would map an edit onto the wrong or
  // multiple cells).
  $: editablePaths = (() => {
    const counts = new Map<string, number>();
    for (const col of columns) counts.set(col.path, (counts.get(col.path) ?? 0) + 1);
    const set = new Set<string>();
    for (const col of columns) {
      if ((counts.get(col.path) ?? 0) === 1 && col.path && col.path !== "$" && !col.path.includes("[")) {
        set.add(col.path);
      }
    }
    return set;
  })();

  // The edit kind for a cell, or "" if not inline-editable. null/container/
  // missing are not inline-editable in v1 (a follow-up).
  function editKindOf(cell: Cell): string {
    switch (cell.kind) {
      case CellKind.STRING: return "string";
      case CellKind.INT: return "int";
      case CellKind.FLOAT: return "float";
      case CellKind.BOOL: return "bool";
      default: return "";
    }
  }

  function isEditable(col: Column, cell: Cell | undefined): boolean {
    return !!cell && editablePaths.has(col.path) && editKindOf(cell) !== "";
  }

  // The ORIGINAL value of a backend cell (kind + exact literal + display).
  function originalValue(cell: Cell): { kind: string; literal: string; display: string } {
    const kind = editKindOf(cell);
    if (kind === "bool") {
      const l = cell.bool === true ? "true" : "false";
      return { kind, literal: l, display: l };
    }
    const s = cell.str ?? "";
    return { kind, literal: s, display: s };
  }

  // The on-screen Row for an index, captured as the edit snapshot.
  function rowSnapshot(index: number): Row {
    return (explorer.rowAt(index).row ?? { index, cells: [] }) as Row;
  }

  let editing: { index: number; path: string; kind: string } | null = null;
  let editText = "";
  let editInvalid = false;

  const JSON_NUMBER = /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/;

  function startEdit(index: number, path: string, cell: Cell): void {
    // Re-entry guard: a double-click INSIDE the open editor (to select a word)
    // bubbles to the cell's on:dblclick. Without this, it would re-run startEdit
    // and reset editText, silently discarding the user's uncommitted typing --
    // so a second double-click on the cell already being edited is a no-op and
    // the browser's native word-selection runs instead.
    if (editing && editing.index === index && editing.path === path) return;
    if (!isEditable(columns[columns.findIndex((c) => c.path === path)], cell)) return;
    const kind = editKindOf(cell);
    if (kind === "bool") {
      // A bool double-click toggles it directly -- no text editor.
      const cur = $explorer.edits?.[index]?.[path];
      const curBool = cur ? cur.value.literal === "true" : cell.bool === true;
      const lit = curBool ? "false" : "true";
      explorer.setEdit(index, path, { kind, literal: lit, display: lit }, originalValue(cell), rowSnapshot(index));
      return;
    }
    const e = $explorer.edits?.[index]?.[path];
    editText = e ? e.value.display : originalValue(cell).display;
    editInvalid = false;
    editing = { index, path, kind };
  }

  function commitEdit(cell: Cell): void {
    if (!editing) return;
    const { index, path, kind } = editing;
    let literal = editText;
    if (kind === "int" || kind === "float") {
      if (!JSON_NUMBER.test(editText.trim())) { editInvalid = true; return; } // reject; keep editing
      literal = editText.trim();
    }
    explorer.setEdit(index, path, { kind, literal, display: literal }, originalValue(cell), rowSnapshot(index));
    editing = null;
    editInvalid = false;
  }

  function cancelEdit(): void { editing = null; editInvalid = false; }

  function onEditorKey(e: KeyboardEvent, cell: Cell): void {
    if (e.key === "Enter") { e.preventDefault(); commitEdit(cell); }
    else if (e.key === "Escape") { e.preventDefault(); cancelEdit(); }
  }

  // Focus + select the editor as it mounts.
  function autofocus(node: HTMLInputElement): void {
    node.focus();
    node.select();
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
      <div class="corner" style="width:{GUTTER_W}px; height:{HEADER_H}px;">
        <input
          class="goto-row"
          type="number"
          min="1"
          max={safeTotal}
          placeholder="#"
          aria-label="Go to row"
          disabled={safeTotal <= 0}
          bind:value={gotoValue}
          on:keydown={onGotoKey}
        />
      </div>
      {#each visibleCols as { c, col } (col.path)}
        <button
          type="button"
          class="header-cell"
          class:focused={col.path === focusPath}
          class:sorted={$explorer.sort?.path === col.path}
          style="left:{GUTTER_W + prefix[c]}px; width:{widths[c]}px; height:{HEADER_H}px;"
          title={col.path}
          on:click={(e) => onHeaderClick(e, col.path)}
        >
          <span class="header-name">{col.name}</span>
          <span
            class="sort-caret"
            class:active={$explorer.sort?.path === col.path}
            role="button"
            tabindex="-1"
            aria-label="Sort by {col.name}"
            title="Sort by {col.name}"
          >{sortGlyph(col.path) || "↕"}</span>
        </button>
      {/each}
    </div>

    <!-- V1: past the height cap the rows layer is NATIVELY position:sticky
         (pinned by Blink on the compositor, like the header) with overflow
         hidden, and rows render window-relative -- never a JS-repositioned
         layer, which would lag the compositor and shear during scroll. Under
         the cap it is exactly today's absolute layer at top:HEADER_H. -->
    <div
      class="rows"
      class:scaled
      style="top:{HEADER_H}px;{scaled ? ` height:${Math.max(0, clientHeight - HEADER_H)}px;` : ''}"
    >
      {#each visibleRows as { i, row } (i)}
        <div
          class="row"
          class:odd={i % 2 === 1}
          role="row"
          data-row-index={i}
          style="top:{rowTopFor(i, effectiveFirstRow, ROW_H, scaled)}px; height:{ROW_H}px; width:{contentWidth}px;"
        >
          <div class="gutter-cell" class:row-edited={row && !!$explorer.edits?.[row.index]} role="rowheader" style="width:{GUTTER_W}px;">
            {#if row}
              {#if $explorer.edits?.[row.index]}<span class="edit-dot" title="Row has edits" aria-hidden="true"></span>{/if}
              {row.index}
            {:else}
              <span class="skeleton-bar gutter-skel"></span>
            {/if}
          </div>
          {#each visibleCols as { c, col } (col.path)}
            {@const edited = row ? $explorer.edits?.[row.index]?.[col.path] : undefined}
            <!-- svelte-ignore a11y-no-static-element-interactions a11y-interactive-supports-focus -->
            <div
              class="data-cell"
              class:edited
              class:editable={row && row.cells[c] && isEditable(col, row.cells[c])}
              role="gridcell"
              style="left:{GUTTER_W + prefix[c]}px; width:{widths[c]}px; height:{ROW_H}px;"
              on:dblclick={() => row && row.cells[c] && startEdit(row.index, col.path, row.cells[c])}
            >
              {#if row && row.cells[c] && editing && editing.index === row.index && editing.path === col.path}
                <!-- E7: the inline editor for a scalar cell (numbers as a text
                     field so a big-int literal is never routed through a JS
                     number). Enter commits, Esc cancels, blur commits. -->
                <input
                  class="cell-editor"
                  class:invalid={editInvalid}
                  aria-label="Edit {col.path}"
                  bind:value={editText}
                  on:keydown={(e) => onEditorKey(e, row.cells[c])}
                  on:blur={() => commitEdit(row.cells[c])}
                  use:autofocus
                />
              {:else if row && edited}
                <!-- E7: an edited cell shows the overlay value, highlighted,
                     with the original in its title. -->
                <span class="edited-value" title="was: {edited.original.display}">{edited.value.display}</span>
              {:else if row && row.cells[c]}
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

  .goto-row {
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    border: none;
    background: transparent;
    color: var(--text-secondary);
    font-size: 11px;
    text-align: center;
    padding: 0 2px;
    -moz-appearance: textfield;
    appearance: textfield;
  }

  .goto-row::-webkit-outer-spin-button,
  .goto-row::-webkit-inner-spin-button {
    -webkit-appearance: none;
    margin: 0;
  }

  .goto-row:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .goto-row:disabled {
    color: var(--text-muted);
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

  .header-cell.sorted {
    color: var(--accent);
  }

  .header-name {
    flex: 1 1 auto;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* E9: the sort caret is quiet (a faint ↕) until the header is hovered or the
     column is the active sort, when it lights up as ▲/▼. */
  .sort-caret {
    flex: 0 0 auto;
    margin-left: var(--space-1);
    font-size: 10px;
    line-height: 1;
    color: var(--text-muted);
    opacity: 0;
  }

  .header-cell:hover .sort-caret,
  .sort-caret.active {
    opacity: 1;
  }

  .sort-caret.active {
    color: var(--accent);
  }

  .rows {
    position: absolute;
    left: 0;
    right: 0;
  }

  /* V1 scaled mode: the rows window is natively pinned to the viewport by Blink
     (position: sticky, like the header) so it does NOT shear during scroll --
     JS only swaps WHICH rows it holds. overflow stays VISIBLE on purpose: an
     overflow!=visible here would make this a scroll container and steal the row
     gutter's horizontal stickiness from the viewport; instead the viewport's own
     overflow clips anything past the fold, and rowWindowFor renders only the
     band rows (+ one partial), so at most ~one row overflows. Its inline `top`
     and `height` come from HEADER_H / the viewport. */
  .rows.scaled {
    position: sticky;
    z-index: 1;
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

  /* E7: editing affordances + highlighting. */
  .data-cell.editable {
    cursor: text;
  }

  .data-cell.edited {
    background: color-mix(in srgb, var(--accent) 14%, transparent);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent) 45%, transparent);
  }

  .edited-value {
    min-width: 0;
    padding: 0 var(--space-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
    color: var(--accent);
    font-weight: 600;
  }

  .cell-editor {
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    border: none;
    outline: 2px solid var(--accent);
    outline-offset: -2px;
    background: var(--surface-1);
    color: var(--text-primary);
    font-size: 13px;
    padding: 0 var(--space-2);
  }

  .cell-editor.invalid {
    outline-color: var(--status-critical);
    background: var(--status-critical-bg);
  }

  .edit-dot {
    display: inline-block;
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--accent);
    margin-right: 4px;
    flex-shrink: 0;
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
