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

  const dispatch = createEventDispatcher<{ focus: { path: string } }>();

  const INDENT = 14;

  $: hasChildren = node.children.length > 0;
  // Rule 4 (T7 brief): a path with no table column has nothing to scroll to
  // -- dim it and take it out of the tab order / click path entirely.
  // columnPaths is the SOLE source of truth here, joined by path, never by
  // index or by node.field-ness (a synthetic interior node just never turns
  // up in columnPaths in practice).
  $: isColumn = columnPaths.has(node.path);
  $: isFocused = focusPath !== "" && node.path === focusPath;

  let expanded = false;
  // Force this node open whenever it sits on the path down to the current
  // focus -- never force it CLOSED, so a user's manual expand/collapse of an
  // unrelated branch survives an unrelated focus change.
  $: if (expandedAncestors.has(node.path)) expanded = true;

  function toggleExpand(): void {
    expanded = !expanded;
  }

  function activate(): void {
    if (!isColumn) return;
    dispatch("focus", { path: node.path });
  }

  // Matches the house keyboard-activation pattern (FieldCard.svelte:37-42).
  function onKeydown(e: KeyboardEvent): void {
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
  tabindex={isColumn ? 0 : -1}
  aria-disabled={!isColumn}
  title={node.path}
  data-path={node.path}
  style="padding-left: {depth * INDENT}px;"
  on:click={activate}
  on:keydown={onKeydown}
>
  {#if hasChildren}
    <button
      type="button"
      class="caret"
      tabindex="-1"
      aria-label={expanded ? "Collapse" : "Expand"}
      on:click|stopPropagation={toggleExpand}
    >
      {expanded ? "▾" : "▸"}
    </button>
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
</div>

{#if hasChildren && expanded}
  {#each node.children as child (child.path)}
    <svelte:self node={child} depth={depth + 1} {focusPath} {columnPaths} {expandedAncestors} on:focus />
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

  .row:not(.dimmed):focus-visible {
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
</style>
