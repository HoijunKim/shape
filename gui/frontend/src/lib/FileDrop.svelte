<script lang="ts">
  import { createEventDispatcher } from "svelte";

  const dispatch = createEventDispatcher<{ open: void }>();
</script>

<div class="drop">
  <div class="drop-inner">
    <p class="drop-title">Drop a data file to profile it</p>
    <button class="primary" on:click={() => dispatch("open")}>Open a file</button>
    <p class="drop-hint">Supports JSON, NDJSON, CSV, TSV, Parquet and SQLite</p>
  </div>
</div>

<style>
  .drop {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
  }

  .drop-inner {
    border: 2px dashed var(--border);
    border-radius: var(--radius);
    padding: 64px 96px;
    text-align: center;
    background: var(--surface-1);
  }

  .drop-title {
    font-size: 16px;
    margin: 0 0 16px;
    color: var(--text-primary);
  }

  .drop-hint {
    margin: 16px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
</style>
