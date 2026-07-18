<script lang="ts">
  // Renders exactly one Cell. All eight CellKind values get their own
  // branch below (spec §3) -- do not collapse any two, even when their
  // markup looks similar (object/array share a shape but a different
  // badge glyph, and must stay distinguishable if that ever changes).
  import type { Cell } from "./types";
  import { CellKind } from "./types";
  import { KIND_TOKEN } from "./kindToken";

  export let cell: Cell;
  export let align: "left" | "right" = "left";

  // KIND_TOKEN lives in kindToken.ts (shared with KindChip.svelte, T7) so
  // there is exactly one int/float->"number" folding table, not a copy per
  // component. Mirrors the guard in charts/TypeMixBar.svelte:12.
  $: token = KIND_TOKEN[cell.kind];
  $: color = token ? `var(--kind-${token})` : "var(--text-muted)";

  // Every Cell field except `kind` is omitempty on the Go side, so "", false
  // and 0 arrive as `undefined` -- read them defensively everywhere below
  // rather than interpolating the optional fields raw (that renders the
  // literal text "undefined").
  $: str = cell.str ?? "";
  $: count = cell.count ?? 0;
  $: boolVal = cell.bool === true;
  $: hasMore = cell.hasMore === true;

  $: titleText =
    cell.kind === CellKind.MISSING ? "missing" :
    cell.kind === CellKind.STRING ? str :
    cell.kind === CellKind.OBJECT || cell.kind === CellKind.ARRAY ? str :
    undefined;
</script>

<div
  class="cell align-{align}"
  class:missing={cell.kind === CellKind.MISSING}
  title={titleText}
>
  {#if cell.kind === CellKind.MISSING}
    <!-- intentionally empty: the diagonal-hatch background (.missing) IS the render -->
  {:else if cell.kind === CellKind.NULL}
    <span class="text null-text" style="color: {color};">null</span>
  {:else if cell.kind === CellKind.BOOL}
    <span class="text mono" style="color: {color};">{boolVal ? "true" : "false"}</span>
  {:else if cell.kind === CellKind.INT || cell.kind === CellKind.FLOAT}
    <!-- cell.str is the exact source literal; cell.num is a lossy float64
         and must never be rendered (project-wide hard constraint). -->
    <span class="text mono" style="color: {color};">{str}</span>
  {:else if cell.kind === CellKind.STRING}
    <span class="text" style="color: {color};">{str}</span>
  {:else if cell.kind === CellKind.OBJECT}
    <span class="text mono" style="color: {color};">{str}{hasMore ? "…" : ""}</span>
    <span class="badge">{"{"}{count}{"}"}</span>
  {:else if cell.kind === CellKind.ARRAY}
    <span class="text mono" style="color: {color};">{str}{hasMore ? "…" : ""}</span>
    <span class="badge">[{count}]</span>
  {/if}
</div>

<style>
  .cell {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    width: 100%;
    height: 100%;
    min-width: 0;
    padding: 0 var(--space-2);
    box-sizing: border-box;
    font-size: 13px;
    color: var(--text-primary);
  }

  .cell.align-right {
    justify-content: flex-end;
  }

  .cell.align-left {
    justify-content: flex-start;
  }

  .cell.missing {
    background-image: repeating-linear-gradient(
      45deg,
      color-mix(in srgb, var(--text-muted) 18%, transparent) 0 4px,
      transparent 4px 9px
    );
  }

  .text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .null-text {
    font-style: italic;
  }

  .mono {
    font-family: var(--font-mono);
  }

  .badge {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 10px;
    line-height: 1.4;
    padding: 0 5px;
    border-radius: var(--radius-sm);
    color: var(--text-secondary);
    background: color-mix(in srgb, var(--text-muted) 16%, transparent);
  }
</style>
