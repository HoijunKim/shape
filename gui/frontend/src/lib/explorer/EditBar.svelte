<script lang="ts">
  // E7 edit toolbar: appears above the table once the overlay holds at least one
  // edit. It reports the count, toggles the "edited only" diff view (a bindable
  // prop App/Explorer route, like filterOpen), reverts everything, and opens the
  // save dialog (a `save` event the parent owns, mirroring how the Export button
  // is wired). It carries no save state itself -- SaveDialog owns that.
  import { createEventDispatcher } from "svelte";
  import { explorer } from "./store";

  // Bindable so the parent can force it off (e.g. Revert all empties the overlay
  // and there is nothing left to show).
  export let editedOnly = false;

  const dispatch = createEventDispatcher<{ save: void }>();

  $: count = $explorer.editedCount;

  function revertAll(): void {
    explorer.revertAllEdits();
    editedOnly = false; // the diff view would otherwise sit on an empty overlay
  }
</script>

{#if count > 0}
  <div class="edit-bar" role="toolbar" aria-label="Edits">
    <span class="count" aria-live="polite">
      {count.toLocaleString()} edited cell{count === 1 ? "" : "s"}
    </span>
    <div class="spacer"></div>
    <button
      type="button"
      class="toggle"
      class:active={editedOnly}
      aria-pressed={editedOnly}
      on:click={() => (editedOnly = !editedOnly)}>Edited only</button>
    <button type="button" on:click={revertAll}>Revert all</button>
    <button type="button" class="primary" on:click={() => dispatch("save")}>Save a copy…</button>
  </div>
{/if}

<style>
  .edit-bar {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
    background: color-mix(in srgb, var(--accent) 8%, var(--surface-2));
    border-bottom: 1px solid var(--border);
  }
  .count {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
    font-variant-numeric: tabular-nums;
  }
  .spacer { flex: 1 1 auto; }
  .edit-bar button {
    flex-shrink: 0;
    padding: 2px var(--space-3);
    font-size: 12px;
  }
  .toggle.active {
    background: var(--accent);
    color: var(--accent-contrast, #fff);
    border-color: var(--accent);
  }
</style>
