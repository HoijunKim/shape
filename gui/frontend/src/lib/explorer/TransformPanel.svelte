<script lang="ts">
  // E4 Task 9: the columns panel -- choose which columns are shown, in what
  // order, under what name (product spec §3.5). It owns a local draft and
  // pushes it to the store through the same 250ms debounce the filter bar
  // uses, so dragging a rename through does not fire a query per keystroke.
  //
  // Mounted by Explorer.svelte beside FilterBar and rendered only when `open`
  // (Header's Columns button, routed through App.svelte's `columnsOpen`).
  import { createEventDispatcher, onDestroy } from "svelte";
  import {
    buildTransform,
    draftErrors,
    draftFromColumns,
    moveColumn,
    projectedColumns,
    type DraftColumn,
  } from "./transformModel";
  import { debounce } from "./debounce";
  import { explorer } from "./store";
  import KindChip from "./KindChip.svelte";
  import type { Column } from "./types";

  // The BASE column set (store.baseColumns), never the projected one: the
  // panel edits the source's columns, and feeding it its own output would
  // make every hidden column unrecoverable.
  export let columns: Column[] = [];
  export let open = false;

  const dispatch = createEventDispatcher<{ errors: string[] }>();

  let draft: DraftColumn[] = draftFromColumns(columns);
  // Re-seed whenever the source's columns change identity (a new file). This
  // is keyed on the array itself, not its length: two files can have the same
  // number of columns.
  let prevColumns: Column[] = columns;
  $: if (columns !== prevColumns) {
    prevColumns = columns;
    draft = draftFromColumns(columns);
    errors = draftErrors(draft);
  }

  let errors: string[] = [];

  const debouncedApply = debounce(
    (payload: { draft: DraftColumn[]; cols: Column[] }) =>
      explorer.setTransform(
        buildTransform(payload.draft, payload.cols),
        projectedColumns(payload.draft, payload.cols),
      ),
    250,
  );

  // Same teardown trap FilterBar documents: opening a second file dips the
  // store's status ready -> opening, unmounting this panel while its 250ms
  // timer is still armed. Without this cancel, file A's projection would be
  // applied to file B, whose columns may not even exist.
  onDestroy(() => debouncedApply.cancel());

  // The store apply is debounced; the error signal is NOT. The export dialog
  // disables its button from these errors, and a guard that lagged the panel's
  // own inline message by 250ms would let a user hit Export on a draft the
  // panel is already complaining about.
  function apply(): void {
    draft = draft; // tell Svelte the array changed (rows mutate in place)
    errors = draftErrors(draft);
    dispatch("errors", errors);
    if (errors.length > 0) {
      // A duplicate or blank name has no correct projection to apply; the
      // engine would reject it too (ExportQuery validates the same rule).
      return;
    }
    debouncedApply.call({ draft, cols: columns });
  }

  function toggle(i: number): void {
    draft[i].visible = !draft[i].visible;
    apply();
  }

  function rename(i: number, value: string): void {
    draft[i].name = value;
    apply();
  }

  function move(i: number, delta: -1 | 1): void {
    draft = moveColumn(draft, i, delta);
    apply();
  }

  function reset(): void {
    draft = draftFromColumns(columns);
    apply();
  }

  $: visibleCount = draft.filter((d) => d.visible).length;
</script>

{#if open}
  <div class="transform-panel">
    <div class="head">
      <span class="title">Columns</span>
      <span class="count">{visibleCount} of {draft.length} shown</span>
      <button type="button" class="reset" on:click={reset}>Reset</button>
    </div>

    {#if errors.length > 0}
      <p class="error" role="alert">{errors[0]}</p>
    {/if}

    <div class="rows">
      {#each draft as row, i (row.path)}
        <div class="column-row">
          <label class="visible">
            <input
              type="checkbox"
              checked={row.visible}
              on:change={() => toggle(i)}
              aria-label="Show {row.path}"
            />
          </label>
          <KindChip kind={columns.find((c) => c.path === row.path)?.type ?? ""} />
          <span class="path mono" title={row.path}>{row.path}</span>
          <input
            class="rename mono"
            type="text"
            value={row.name}
            placeholder={row.path}
            aria-label="Name for {row.path}"
            on:input={(e) => rename(i, e.currentTarget.value)}
          />
          <button
            type="button"
            class="move"
            aria-label="Move {row.path} up"
            disabled={i === 0}
            on:click={() => move(i, -1)}>↑</button
          >
          <button
            type="button"
            class="move"
            aria-label="Move {row.path} down"
            disabled={i === draft.length - 1}
            on:click={() => move(i, 1)}>↓</button
          >
        </div>
      {/each}
    </div>
  </div>
{/if}

<style>
  /* Mirrors FilterBar's bar (surface-1 face, hairline top border, the app.css
     spacing scale) so the two panels read as one system. */
  .transform-panel {
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

  .head {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .title {
    font-weight: 600;
    font-size: 13px;
    color: var(--text-primary);
  }

  .count {
    flex: 1 1 auto;
    font-size: 12px;
    color: var(--text-muted);
  }

  .error {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    font-size: 12px;
    border-radius: var(--radius-sm);
  }

  .rows {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .column-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }

  .visible {
    display: inline-flex;
    align-items: center;
  }

  .path {
    flex: 0 1 22ch;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    color: var(--text-muted);
  }

  /* Hand-styled to match the global button rule, exactly as ConditionRow's
     inputs are -- app.css has no input tokens. */
  .rename {
    flex: 1 1 auto;
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-1);
    color: var(--text-primary);
    padding: var(--space-2) var(--space-3);
    font-size: 12px;
  }

  .rename:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .move {
    flex-shrink: 0;
    padding: 2px var(--space-2);
    font-size: 12px;
    line-height: 1.2;
  }
</style>
