<script lang="ts">
  // T8: the header now shows the explorer's source/tier instead of the old
  // profiler dashboard's record/skipped counts -- there is no ProfileFile
  // model any more, just $explorer's path/tier/format (App.svelte passes
  // these through directly rather than this component subscribing to the
  // store itself, so it stays a plain, storeless presentational component).
  import { createEventDispatcher } from "svelte";

  export let path = "";
  export let tier = "";
  export let format = "";
  export let canExport = false;
  export let theme: "light" | "dark" = "light";
  export let filterOpen = false;
  // E4: the columns panel's toggle state, owned by App.svelte exactly like
  // filterOpen (Header and Explorer are siblings there).
  export let columnsOpen = false;
  // E5: the jq/SQL panel's toggle, owned by App.svelte like the others.
  export let codeOpen = false;
  // E11: the saved-views menu toggle. Owned by App (global, works before a file
  // is open), so it is a plain toggle like codeOpen.
  export let viewsOpen = false;

  const dispatch = createEventDispatcher<{
    open: void;
    export: void;
    toggleTheme: void;
    toggleFilter: void;
    toggleColumns: void;
    toggleCode: void;
    toggleViews: void;
    exportData: void;
  }>();

  $: fileName = path ? path.replace(/^.*[\\/]/, "") : "";
</script>

<header class="header">
  <div class="title">
    <span class="app-name">shape</span>
    {#if fileName}
      <span class="source mono" title={path}>{fileName}</span>
      <span class="counts">
        {#if tier}<span class="tier">{tier}</span>{/if}
        {#if tier && format}<span class="sep" aria-hidden="true">·</span>{/if}
        {#if format}<span class="format">{format}</span>{/if}
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
    <button
      on:click={() => dispatch("toggleColumns")}
      aria-pressed={columnsOpen}
      title="Choose, reorder and rename columns"
    >
      Columns
    </button>
    <button
      on:click={() => dispatch("toggleFilter")}
      aria-pressed={filterOpen}
      title="Filter"
    >
      Filter
    </button>
    <button
      on:click={() => dispatch("toggleCode")}
      aria-pressed={codeOpen}
      title="Show the equivalent jq and SQL"
    >
      Code
    </button>
    <button
      on:click={() => dispatch("toggleViews")}
      aria-pressed={viewsOpen}
      title="Save and apply views (filter, search, sort, reshape)"
    >
      Views
    </button>
    <button on:click={() => dispatch("open")}>Open</button>
    <!-- The pre-E4 "Export schema" action, kept working and demoted to a
         plain button so the primary slot belongs to the DATA export. -->
    <button disabled={!canExport} on:click={() => dispatch("export")} title="Export the inferred JSON Schema">
      Schema
    </button>
    <button class="primary" disabled={!canExport} on:click={() => dispatch("exportData")}>
      Export
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
    display: flex;
    align-items: center;
    gap: var(--space-2);
    color: var(--text-muted);
    font-size: 12px;
    white-space: nowrap;
  }

  .sep {
    color: var(--border);
  }

  /* A3: matches StatusBar's .tier rule -- the same `tier` string (e.g.
     "memory"/"rescan") is rendered lowercase here and uppercased there; this
     keeps the two presentations consistent. */
  .tier {
    text-transform: uppercase;
    letter-spacing: 0.02em;
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
