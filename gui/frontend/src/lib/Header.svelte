<script lang="ts">
  import { createEventDispatcher } from "svelte";

  export let source = "";
  export let records = 0;
  export let skipped = 0;
  export let canExport = false;

  const dispatch = createEventDispatcher<{ open: void; export: void }>();

  $: fileName = source ? source.replace(/^.*[\\/]/, "") : "";
</script>

<header class="header">
  <div class="title">
    <span class="app-name">shape</span>
    {#if fileName}
      <span class="source mono" title={source}>{fileName}</span>
      <span class="counts">
        {records.toLocaleString()} records
        {#if skipped > 0}<span class="skipped">- {skipped.toLocaleString()} skipped</span>{/if}
      </span>
    {/if}
  </div>
  <div class="actions">
    <button on:click={() => dispatch("open")}>Open</button>
    <button class="primary" disabled={!canExport} on:click={() => dispatch("export")}>
      Export schema
    </button>
  </div>
</header>

<style>
  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-panel);
    flex-shrink: 0;
  }

  .title {
    display: flex;
    align-items: baseline;
    gap: 12px;
    min-width: 0;
  }

  .app-name {
    font-weight: 700;
    font-size: 15px;
  }

  .source {
    color: var(--text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 32ch;
  }

  .counts {
    color: var(--text-muted);
    font-size: 12px;
    white-space: nowrap;
  }

  .skipped {
    color: var(--danger);
  }

  .actions {
    display: flex;
    gap: 8px;
    flex-shrink: 0;
  }
</style>
