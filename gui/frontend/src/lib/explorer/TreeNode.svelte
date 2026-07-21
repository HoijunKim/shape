<script lang="ts">
  // One row of the structure-map sidebar, recursive via <svelte:self> for
  // children. Renders FieldDTO data straight from $explorer.fields/buildTree
  // -- never internal/visual's FieldCard shape (T7 rule).
  import { createEventDispatcher } from "svelte";
  import type { TreeNode } from "./tree";
  import KindChip from "./KindChip.svelte";
  import Meter from "../charts/Meter.svelte";
  import Badge from "../charts/Badge.svelte";
  import { dominantKind, nullStatus, formatPercent, formatDistinct } from "./fieldDisplay";

  export let node: TreeNode;
  export let depth: number;
  export let focusPath: string;
  export let columnPaths: Set<string>;
  // Paths that must be force-expanded because a focused descendant lives
  // under them (StructureMap computes this once per focusPath change via
  // fieldDisplay.ancestorPaths and threads it down through every level).
  export let expandedAncestors: Set<string>;
  // Bump this (e.g. to a store revision counter) to force a fresh check of
  // expandedAncestors even when focusPath's own STRING value is unchanged.
  // Svelte's reactivity is value-based: re-assigning a prop to an
  // already-equal value is a silent no-op, so a repeated focus of an
  // already-focused path (the user clicks the same DataTable header again
  // after manually collapsing an ancestor in between) would otherwise never
  // re-run the force-expand check below and the row would stay hidden
  // forever (Minor 4). Defaults to 0 so no existing caller needs to touch
  // it. Deliberately one-way (read-only here) -- it must never be written
  // back to, and it never touches focusPath itself, so it cannot loop.
  export let focusToken = 0;

  const dispatch = createEventDispatcher<{
    focus: { path: string };
    seedFilter: { path: string; type: string };
  }>();

  const INDENT = 14;

  $: hasChildren = node.children.length > 0;
  // Rule 4 (T7 brief): a path with no table column has nothing to scroll to
  // -- dim it and take it out of the FOCUS click path entirely. columnPaths
  // is the SOLE source of truth here, joined by path, never by index or by
  // node.field-ness: a node can carry its own FieldDTO (a profiled interior
  // object, or an array-element path like "items[]"/"items[].sku") and
  // still be correctly absent from columnPaths -- internal/query/
  // columns.go's pure-interior-object rule and its unconditional exclusion
  // of Elem-segment paths (see StructureMap.test.ts's "items[]" fixture).
  $: isColumn = columnPaths.has(node.path);
  $: isFocused = focusPath !== "" && node.path === focusPath;
  // A dimmed parent still has children worth browsing (Finding 3): tabindex
  // must not be gated on isColumn alone, only the "activate as column"
  // action (Enter/Space, click-outside-the-caret) is. Anything with
  // children, or any column (even a childless leaf), stays reachable; a
  // dimmed leaf has nothing to do and correctly drops out of the tab order.
  $: canReachByTab = isColumn || hasChildren;

  let expanded = false;
  // Force this node open whenever it sits on the path down to the current
  // focus -- never force it CLOSED, so a user's manual expand/collapse of an
  // unrelated branch survives an unrelated focus change. Also re-checked on
  // every focusToken bump alone (see the export above): that is the escape
  // hatch for re-revealing a branch the user collapsed by hand when the
  // SAME path is focused again.
  $: {
    void focusToken;
    if (expandedAncestors.has(node.path)) expanded = true;
  }

  function toggleExpand(): void {
    expanded = !expanded;
  }

  function activate(): void {
    if (!isColumn) return;
    dispatch("focus", { path: node.path });
  }

  // E3 Task 9 (click-to-seed, the second "wow"): the funnel button only ever
  // renders on isColumn rows (see the template below), so this is never
  // reachable for a column-less node. stopPropagation is load-bearing, not
  // decorative: the row itself is the sole interactive control (comment
  // above onRowClick) and its own on:click handler runs during the bubble
  // phase, AFTER this button's own click handler -- without stopPropagation
  // the click would also reach onRowClick and fire a `focus` dispatch on top
  // of `seedFilter`, scrolling DataTable's column into view at the same time
  // the filter bar opens (a plan review flagged exactly this double-fire).
  function onSeedClick(e: MouseEvent): void {
    e.stopPropagation();
    dispatch("seedFilter", { path: node.path, type: node.field ? dominantKind(node.field) : "string" });
  }

  // The caret is a plain aria-hidden span, not a nested interactive element
  // (a <button> inside a role="button" row is invalid nesting -- Finding 3),
  // so the ROW is the sole interactive control and click-delegates by
  // target: a click landing on the caret toggles expand/collapse, anywhere
  // else on the row activates the column focus (Rule 4).
  function onRowClick(e: MouseEvent): void {
    const eventTarget = e.target as HTMLElement;
    if (hasChildren && eventTarget.closest(".caret")) {
      toggleExpand();
      return;
    }
    // A dimmed parent has no column to focus, so a body click would other-
    // wise be a dead click and its only mouse affordance would be the 14px
    // caret glyph. Give the whole row expand/collapse instead.
    if (!isColumn && hasChildren) {
      toggleExpand();
      return;
    }
    activate();
  }

  // Matches the house keyboard-activation pattern (FieldCard.svelte:37-42)
  // for Enter/Space. ArrowRight/ArrowLeft expand/collapse (Finding 3) and
  // are available on ANY node with children regardless of isColumn -- a
  // dimmed parent still has structure worth browsing -- but focus dispatch
  // (Enter/Space) stays gated on isColumn, matching Rule 4.
  function onKeydown(e: KeyboardEvent): void {
    // The seed funnel is a real focusable <button> nested in this row. A
    // keydown on it bubbles here; without this guard the row would also
    // preventDefault()+activate() on Enter/Space, stealing focus and (for
    // Enter) suppressing the button's own click so seedFilter never fires.
    // Let the button handle its own keyboard activation (native <button>
    // fires click on Enter/Space -> onSeedClick).
    if ((e.target as HTMLElement).closest(".seed")) return;
    if (hasChildren && e.key === "ArrowRight") {
      e.preventDefault();
      if (!expanded) expanded = true;
      return;
    }
    if (hasChildren && e.key === "ArrowLeft") {
      e.preventDefault();
      if (expanded) expanded = false;
      return;
    }
    if (!isColumn) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      activate();
    }
  }

  $: kind = node.field ? dominantKind(node.field) : "";
  $: presenceRate = node.field?.presence ?? 0;
  $: nullRate = node.field?.nullRate ?? 0;
  $: presenceText = node.field ? formatPercent(presenceRate) : "";
  $: nullText = node.field ? formatPercent(nullRate) : "";
  $: nullStat = node.field ? nullStatus(nullRate) : "";
  $: distinctText = node.field ? formatDistinct(node.field.distinct, node.field.distinctExact) : "";
</script>

<div
  class="row"
  class:focused={isFocused}
  class:dimmed={!isColumn}
  role="button"
  tabindex={canReachByTab ? 0 : -1}
  aria-disabled={!canReachByTab}
  aria-expanded={hasChildren ? expanded : undefined}
  title={node.path}
  data-path={node.path}
  style="padding-left: {depth * INDENT}px;"
  on:click={onRowClick}
  on:keydown={onKeydown}
>
  {#if hasChildren}
    <span class="caret" aria-hidden="true">{expanded ? "▾" : "▸"}</span>
  {:else}
    <span class="caret-spacer" aria-hidden="true"></span>
  {/if}

  <span class="name">{node.name}</span>

  {#if node.field}
    <KindChip {kind} />
    <div class="meter-slot">
      <Meter {presenceRate} {nullRate} {presenceText} {nullText} nullStatus={nullStat} />
    </div>
    <span class="distinct">{distinctText}</span>
    {#if node.field.drift}
      <Badge severity="warning" icon="⚠" label="drift" />
    {/if}
  {/if}

  {#if isColumn}
    <button
      type="button"
      class="seed"
      aria-label="Add filter for {node.path}"
      title="Add filter for {node.path}"
      on:click={onSeedClick}
    >
      <svg viewBox="0 0 16 16" width="11" height="11" aria-hidden="true" focusable="false">
        <path d="M1.5 2h13l-5 6v4.5l-3 1.5v-6z" />
      </svg>
    </button>
  {/if}
</div>

{#if hasChildren && expanded}
  {#each node.children as child (child.path)}
    <svelte:self
      node={child}
      depth={depth + 1}
      {focusPath}
      {columnPaths}
      {expandedAncestors}
      {focusToken}
      on:focus
      on:seedFilter
    />
  {/each}
{/if}

<style>
  .row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 26px;
    padding: 2px var(--space-2) 2px 0;
    box-sizing: border-box;
    border-radius: var(--radius-sm);
    outline: none;
  }

  .row:not(.dimmed) {
    cursor: pointer;
  }

  .row.dimmed {
    cursor: default;
    opacity: 0.45;
  }

  .row:not(.dimmed):hover {
    background: var(--surface-2);
  }

  /* Any row with tabindex=0 is keyboard-operable (a column can be focused,
     or -- Finding 3 -- a dimmed parent's children can still be browsed via
     ArrowRight/ArrowLeft even though it can't be activated), so the visible
     focus ring is gated on actual reachability, not on isColumn/.dimmed. */
  .row[tabindex="0"]:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .row.focused {
    background: color-mix(in srgb, var(--accent) 14%, var(--surface-1));
    box-shadow: inset 3px 0 0 var(--accent);
  }

  .caret,
  .caret-spacer {
    flex-shrink: 0;
    width: 14px;
    height: 14px;
  }

  .caret {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    margin: 0;
    border: none;
    border-radius: 0;
    background: transparent;
    color: var(--text-muted);
    font-size: 10px;
    cursor: pointer;
  }

  .name {
    flex: 1 1 auto;
    min-width: 24px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    color: var(--text-primary);
  }

  .meter-slot {
    flex: 0 0 56px;
    min-width: 0;
  }

  .distinct {
    flex-shrink: 0;
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
  }

  /* Quiet by default (a dense tree of funnel icons on every row would be
     noisy) but never fully hidden -- opacity, not display:none, so it stays
     discoverable and clickable without requiring a hover step first. */
  .seed {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    margin: 0;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text-muted);
    opacity: 0.4;
    cursor: pointer;
  }

  .seed svg {
    fill: currentColor;
  }

  .row:hover .seed,
  .seed:hover,
  .seed:focus-visible {
    opacity: 1;
  }

  .seed:hover {
    color: var(--accent);
    background: var(--surface-2);
  }

  .seed:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
