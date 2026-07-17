<script lang="ts">
  import { createEventDispatcher } from "svelte";
  import type { visual } from "../../wailsjs/go/models";

  export let summary: visual.Summary | null = null;
  export let canExport = false;
  export let theme: "light" | "dark" = "light";

  const dispatch = createEventDispatcher<{ open: void; export: void; toggleTheme: void }>();

  $: fileName = summary?.name ? summary.name.replace(/^.*[\\/]/, "") : "";
</script>

<header class="header">
  <div class="title">
    <span class="app-name">shape</span>
    {#if summary && fileName}
      <span class="source mono" title={summary.name}>{fileName}</span>
      <span class="counts">
        {summary.records.toLocaleString()} records
        {#if summary.skipped > 0}
          <span class="skipped">- {summary.skipped.toLocaleString()} skipped</span>
        {/if}
        <span class="format">- {summary.format}</span>
      </span>
    {/if}
  </div>
  <div class="actions">
    <button
      class="theme-toggle"
      title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
      aria-label="Toggle theme"
      on:click={() => dispatch("toggleTheme")}
    >
      {theme === "dark" ? "☀" : "☾"}
    </button>
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
    gap: var(--space-3);
    padding: var(--space-2) var(--space-4);
    border-bottom: 1px solid var(--border);
    background: var(--surface-1);
    flex-shrink: 0;
  }

  .title {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
    min-width: 0;
  }

  .app-name {
    flex-shrink: 0;
    font-weight: 700;
    font-size: 15px;
    color: var(--text-primary);
  }

  .source {
    min-width: 0;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 32ch;
  }

  .counts {
    flex-shrink: 0;
    color: var(--text-muted);
    font-size: 12px;
    white-space: nowrap;
  }

  .skipped {
    color: var(--status-warning);
  }

  .format {
    text-transform: uppercase;
    letter-spacing: 0.02em;
  }

  .actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex-shrink: 0;
  }

  .theme-toggle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    padding: var(--space-2) 0;
    font-size: 14px;
    line-height: 1;
  }
</style>
