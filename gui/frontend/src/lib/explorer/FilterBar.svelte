<script lang="ts">
  // Task 7 (E3): assembles Task 2's draft model (filterModel.ts), Task 3's
  // debounce, and Task 6's ConditionRow into the actual filter bar, and wires
  // it to the `explorer` store's setFilter -- the first end-to-end path from
  // a UI edit to a live-filtered query (spec §3.4/§5). Mounted by
  // Explorer.svelte only once $explorer.status === "ready" (a column list is
  // needed for "+ Add condition" and each row's column <select>), and only
  // rendered when `open` is true (Header's Filter button, routed through
  // App.svelte's `filterOpen`, toggles this).
  import { onDestroy } from "svelte";
  import { emptyDraft, newCondition, buildFilter } from "./filterModel";
  import type { FilterDraft, DraftCondition } from "./filterModel";
  import { debounce } from "./debounce";
  import { explorer } from "./store";
  import ConditionRow from "./ConditionRow.svelte";
  import type { Column } from "./types";

  export let columns: Column[] = [];
  export let open = false;

  let draft: FilterDraft = emptyDraft();
  let nextId = 1;

  // One debounce is enough for E3 (spec's count/apply split note): setFilter
  // itself drives both the page-0 refetch and (Task 5) the live count.
  const debouncedApply = debounce((f) => explorer.setFilter(f), 250);

  // Teardown safety (review F1): opening a second file dips the store's
  // status ready -> opening, which unmounts THIS component (Explorer's
  // `{#if $explorer.status === "ready"}`) and later mounts a fresh, empty
  // one. Without cancelling here, this instance's still-armed 250ms timer
  // would survive its own destroy and fire explorer.setFilter(f_thisDraft)
  // against the NEW file's handle once it elapses -- filtering file B by a
  // condition typed against file A, while the new bar shows nothing. The
  // Svelte `{#if}` teardown runs in a microtask flush, well before the 250ms
  // macrotask timer, so cancel() here reliably wins the race.
  onDestroy(() => debouncedApply.cancel());

  // buildFilter (filterModel.ts) omits every incomplete/invalid condition,
  // so a half-typed regex or an empty value simply never reaches the built
  // Filter -- rebuild() never needs to pre-check that itself.
  function rebuild(): void {
    debouncedApply.call(buildFilter(draft));
  }

  function addCondition(): void {
    if (columns.length === 0) return; // nothing to default a fresh row's column to
    draft = {
      ...draft,
      conditions: [...draft.conditions, newCondition(nextId++, columns[0].path, columns[0].type)],
    };
    rebuild();
  }

  function onRowChange(e: CustomEvent<DraftCondition>): void {
    const updated = e.detail;
    draft = {
      ...draft,
      conditions: draft.conditions.map((c) => (c.id === updated.id ? updated : c)),
    };
    rebuild();
  }

  function onRowRemove(e: CustomEvent<{ id: number }>): void {
    const { id } = e.detail;
    draft = { ...draft, conditions: draft.conditions.filter((c) => c.id !== id) };
    rebuild();
  }

  function onCombinatorChange(e: Event): void {
    draft = { ...draft, combinator: (e.target as HTMLSelectElement).value as "and" | "or" };
    rebuild();
  }

  function clear(): void {
    draft = emptyDraft();
    rebuild();
  }
</script>

{#if open}
  <div class="filter-bar">
    {#if draft.conditions.length > 0}
      <div class="rows">
        {#each draft.conditions as condition (condition.id)}
          <ConditionRow {condition} {columns} on:change={onRowChange} on:remove={onRowRemove} />
        {/each}
      </div>
    {/if}
    <div class="controls">
      <select
        class="combinator"
        aria-label="Combinator"
        value={draft.combinator}
        on:change={onCombinatorChange}
      >
        <option value="and">AND</option>
        <option value="or">OR</option>
      </select>
      <button
        type="button"
        class="add-condition"
        disabled={columns.length === 0}
        on:click={addCondition}
      >
        + Add condition
      </button>
      <button type="button" class="clear" on:click={clear}>Clear</button>
    </div>
  </div>
{/if}

<style>
  /* Matches ConditionRow's own row styling (Task 6): surface-1 face, a
     hairline border, and the app.css spacing scale, so the bar reads as one
     system with the rows it contains rather than a divergent container. */
  .filter-bar {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    background: var(--surface-1);
    border-top: 1px solid var(--border);
    max-height: 45vh;
    overflow-y: auto;
    box-sizing: border-box;
  }

  .rows {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .controls {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .combinator {
    font-family: inherit;
    font-size: inherit;
    color: var(--text-primary);
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: var(--space-2) var(--space-3);
  }

  .combinator:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
