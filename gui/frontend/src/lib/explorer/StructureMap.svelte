<script lang="ts">
  // The structure-map sidebar (T7, spec §3.2): the profiler demoted from
  // "the product" to a navigation tree that drives column focus in
  // DataTable. Renders OpenResult.Profile.fields (FieldDTO) via buildTree --
  // never internal/visual's FieldCard, and no second profiling pass.
  import { createEventDispatcher } from "svelte";
  import type { FieldDTO } from "./types";
  import { buildTree } from "./tree";
  import { ancestorPaths } from "./fieldDisplay";
  import TreeNode from "./TreeNode.svelte";

  export let fields: FieldDTO[] = [];
  export let focusPath = "";
  export let columnPaths: Set<string> = new Set();

  const dispatch = createEventDispatcher<{ focus: { path: string } }>();

  $: tree = buildTree(fields);
  // Recomputed fresh every time focusPath changes (a sidebar click OR a
  // DataTable header click -- focus is bidirectional, T7 rule) so ancestors
  // of whichever path is now focused get force-expanded, wherever the
  // change came from.
  $: expandedAncestors = ancestorPaths(focusPath);

  function onFocus(e: CustomEvent<{ path: string }>): void {
    dispatch("focus", e.detail);
  }
</script>

<nav class="structure-map" aria-label="Field structure">
  {#if tree.length === 0}
    <p class="empty">No fields.</p>
  {:else}
    {#each tree as node (node.path)}
      <TreeNode {node} depth={0} {focusPath} {columnPaths} {expandedAncestors} on:focus={onFocus} />
    {/each}
  {/if}
</nav>

<style>
  .structure-map {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow-y: auto;
    overflow-x: hidden;
    box-sizing: border-box;
    padding: var(--space-2);
    background: var(--surface-1);
    border-right: 1px solid var(--border);
  }

  .empty {
    margin: 0;
    padding: var(--space-4);
    color: var(--text-muted);
    font-size: 12px;
  }
</style>
