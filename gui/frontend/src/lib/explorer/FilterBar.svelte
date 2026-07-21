<script lang="ts">
  // Task 7 (E3): assembles Task 2's draft model (filterModel.ts), Task 3's
  // debounce, and Task 6's ConditionRow into the actual filter bar, and wires
  // it to the `explorer` store's setFilter -- the first end-to-end path from
  // a UI edit to a live-filtered query (spec §3.4/§5). Mounted by
  // Explorer.svelte only once $explorer.status === "ready" (a column list is
  // needed for "+ Add condition" and each row's column <select>), and only
  // rendered when `open` is true (Header's Filter button, routed through
  // App.svelte's `filterOpen`, toggles this).
  import { onDestroy, tick } from "svelte";
  import { emptyDraft, newCondition, buildFilter } from "./filterModel";
  import type { FilterDraft, DraftCondition } from "./filterModel";
  import { debounce } from "./debounce";
  import { explorer } from "./store";
  import ConditionRow from "./ConditionRow.svelte";
  import type { Column, Filter } from "./types";

  export let columns: Column[] = [];
  export let open = false;
  // E3 Task 9 (click-to-seed): a sidebar funnel click seeds a fresh condition
  // for `seed.path`/`seed.type`. `nonce` (not path/type alone) is what the
  // reactive guard below keys off -- seeding the SAME path twice in a row
  // (e.g. the user clicks the same field's funnel again after removing the
  // condition it created) must still append a new row each time, and two
  // equal-value prop re-assignments in a row would otherwise be indistinguish-
  // able from Svelte's point of view (the whole reason focusToken/resetToken
  // elsewhere in this codebase use the same bump-a-counter pattern instead of
  // re-signalling via a value that might repeat).
  export let seed: { path: string; type: string; nonce: number } | null = null;

  let draft: FilterDraft = emptyDraft();
  let nextId = 1;
  let barEl: HTMLDivElement | undefined;

  // One debounce is enough for E3 (spec's count/apply split note): setFilter
  // itself drives both the page-0 refetch and (Task 5) the live count.
  const debouncedApply = debounce((f: Filter) => explorer.setFilter(f), 250);

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

  // E3 Task 9: seed a fresh condition from a sidebar funnel click. Guarded by
  // a prevSeedNonce sentinel initialized to -1 (never a real nonce, which
  // Explorer.svelte starts at 0 and only increments) so mounting with `seed`
  // already non-null (a prop re-pass carrying the SAME seed, which Svelte's
  // safe_not_equal treats as "changed" for any object-typed prop regardless
  // of identity/content -- see Explorer.test.ts's focusToken test for the
  // same caveat) never re-seeds; only an actual nonce bump does.
  let prevSeedNonce = -1;
  $: if (seed && seed.nonce !== prevSeedNonce) {
    prevSeedNonce = seed.nonce;
    seedCondition(seed.path, seed.type);
  }

  function seedCondition(path: string, type: string): void {
    draft = {
      ...draft,
      conditions: [...draft.conditions, newCondition(nextId++, path, type)],
    };
    rebuild();
    void focusLastValueInput();
  }

  // The newly-seeded row is always the LAST one (seeding only ever appends),
  // so the last `.condition-row` in the DOM is unambiguously it -- no id
  // needs threading through ConditionRow (which Task 9 does not touch) to
  // find it. Waits a tick for `open`/the new row to actually mount (the
  // funnel click sets `filterOpen` and `seed` together, and the bar may have
  // been closed, i.e. unmounted, until this same update). Isnull/notnull
  // (container-type seeds) render no "Value" control at all -- there is
  // nothing to focus, so this silently no-ops.
  async function focusLastValueInput(): Promise<void> {
    await tick();
    if (!barEl) return;
    const rows = barEl.querySelectorAll(".condition-row");
    const last = rows[rows.length - 1] as HTMLElement | undefined;
    const valueEl = last?.querySelector('[aria-label="Value"]') as HTMLElement | null;
    valueEl?.focus();
  }
</script>

{#if open}
  <div class="filter-bar" bind:this={barEl}>
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
