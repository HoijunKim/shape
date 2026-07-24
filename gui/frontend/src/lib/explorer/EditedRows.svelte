<script lang="ts">
  // E7 "Show edited only": a non-virtualized diff list of every edited cell,
  // grouped by row and sorted by absolute index. It reads the overlay
  // ($explorer.edits) directly -- NOT the DataTable's contiguous-index band --
  // so it works no matter where the edited rows sit in a huge file, and each
  // entry already carries the original + new value (recorded at edit time), so
  // nothing is re-fetched. Each cell can be reverted in place.
  import { explorer } from "./store";
  import { KIND_TOKEN } from "./kindToken";
  import type { EditEntry } from "./store";

  // Sorted [index, {path: entry}] pairs. Rebuilt whenever the overlay changes
  // identity (setEdit/revert return a fresh edits object), so a revert here
  // drops its row group live.
  $: rows = Object.keys($explorer.edits)
    .map(Number)
    .sort((a, b) => a - b)
    .map((index) => ({
      index,
      cells: Object.entries($explorer.edits[index]) as [string, EditEntry][],
    }));

  function tokenVar(kind: string): string {
    const token = KIND_TOKEN[kind];
    return token ? `var(--kind-${token})` : "var(--text-muted)";
  }
</script>

<div class="edited-rows" role="region" aria-label="Edited cells">
  {#if rows.length === 0}
    <div class="empty" role="status">
      <p>No edits yet</p>
      <p class="hint">Double-click a scalar cell in the table to edit it.</p>
    </div>
  {:else}
    {#each rows as { index, cells } (index)}
      <div class="row-group">
        <div class="row-head">Row {index.toLocaleString()}</div>
        <ul class="cells">
          {#each cells as [path, entry] (path)}
            <li class="cell-diff">
              <span class="path" title={path}>{path}</span>
              <span class="was" title="Original value">{entry.original.display}</span>
              <span class="arrow" aria-hidden="true">→</span>
              <span class="now" style="color:{tokenVar(entry.value.kind)}" title="New value">{entry.value.display}</span>
              <button
                type="button"
                class="revert"
                title="Revert this cell"
                aria-label="Revert {path} on row {index}"
                on:click={() => explorer.revertCell(index, path)}>↺</button>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  {/if}
</div>

<style>
  .edited-rows {
    height: 100%;
    overflow-y: auto;
    padding: var(--space-3);
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    color: var(--text-muted);
    text-align: center;
  }
  .empty p { margin: 0; }
  .empty .hint { font-size: 12px; }
  .row-group {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-1);
    overflow: hidden;
  }
  .row-head {
    padding: var(--space-2) var(--space-3);
    background: var(--surface-2);
    border-bottom: 1px solid var(--border);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }
  .cells { list-style: none; margin: 0; padding: 0; }
  .cell-diff {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
    font-size: 13px;
    border-top: 1px solid color-mix(in srgb, var(--border) 55%, transparent);
  }
  .cell-diff:first-child { border-top: none; }
  .path {
    flex: 0 0 auto;
    max-width: 40%;
    font-family: var(--font-mono, monospace);
    font-size: 12px;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .was {
    color: var(--text-muted);
    text-decoration: line-through;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .arrow { flex: 0 0 auto; color: var(--text-muted); }
  .now {
    flex: 1 1 auto;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .revert {
    flex: 0 0 auto;
    padding: 0 var(--space-2);
    font-size: 13px;
    line-height: 1.4;
  }
</style>
