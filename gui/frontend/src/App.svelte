<script lang="ts">
  import { onMount } from "svelte";
  import { SchemaJSON, OpenFileDialog, SaveText } from "../wailsjs/go/main/App";
  import { OnFileDrop, OnFileDropOff } from "../wailsjs/runtime/runtime";
  import Header from "./lib/Header.svelte";
  import Explorer from "./lib/explorer/Explorer.svelte";
  import ViewsMenu from "./lib/explorer/ViewsMenu.svelte";
  import { explorer } from "./lib/explorer/store";

  // T8: the P3 dashboard (KpiRow/FieldGrid/FieldDetail) and the ProfileFile
  // binding are no longer routed to here -- Explorer (backed by the
  // OpenSource/QueryRows query engine) is the only view after a file opens.
  // Those components stay in the repo, compiling and tested, per the locked
  // product decision; they are simply not imported any more.
  let theme: "light" | "dark" = "light";
  // Export failures are a distinct, narrower concern from an explorer open()
  // failure (which the store already tracks as $explorer.error/status
  // itself, rendered by Explorer's own error state) -- kept as a small local
  // alert here rather than folded into the store.
  let exportError = "";
  // E3 Task 7: Header and Explorer are SIBLINGS here, and Explorer does not
  // render Header -- so this flag (and the toggle event routing it) must
  // live here, the only place that mounts both. `bind:filterOpen` on
  // Explorer lets Task 9's seed set it from outside and have it propagate
  // back up to Header's aria-pressed.
  let filterOpen = false;
  // E4: the same ownership rule for the columns panel and the export dialog.
  // Both are bindable on Explorer so it can close the dialog itself.
  let columnsOpen = false;
  let exportOpen = false;
  let codeOpen = false;
  let viewsOpen = false; // E11: saved-views menu

  async function load(path: string): Promise<void> {
    if (!path) return;
    await explorer.open(path);
  }

  async function open(): Promise<void> {
    // OpenFileDialog resolves to "" when the user cancels; load() no-ops on
    // an empty path, so cancellation is silently ignored.
    await load(await OpenFileDialog());
  }

  async function exportSchema(): Promise<void> {
    const path = $explorer.path;
    if (!path) return;
    exportError = "";
    try {
      const schema = await SchemaJSON(path);
      await SaveText("schema.json", schema);
    } catch (e) {
      exportError = String(e);
    }
  }

  function toggleTheme(): void {
    theme = theme === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = theme;
  }

  onMount(() => {
    if (window.matchMedia?.("(prefers-color-scheme: dark)").matches) {
      theme = "dark";
    }

    OnFileDrop((_x, _y, paths) => {
      if (paths?.length === 1) {
        load(paths[0]);
      } else if (paths?.length >= 2) {
        // SEAM for a future two-file visual diff: drop target should call
        // DiffFiles(paths[0], paths[1]) and switch to a DiffView. Until that
        // lands, just open the first dropped file so a two-file drop still
        // does something useful rather than nothing.
        load(paths[0]);
      }
    }, true);

    return () => OnFileDropOff();
  });
</script>

<main class="app">
  <Header
    path={$explorer.path}
    tier={$explorer.tier}
    format={$explorer.format}
    canExport={$explorer.status === "ready"}
    {theme}
    {filterOpen}
    {columnsOpen}
    {codeOpen}
    {viewsOpen}
    on:open={open}
    on:export={exportSchema}
    on:toggleTheme={toggleTheme}
    on:toggleFilter={() => (filterOpen = !filterOpen)}
    on:toggleColumns={() => (columnsOpen = !columnsOpen)}
    on:toggleCode={() => (codeOpen = !codeOpen)}
    on:toggleViews={() => (viewsOpen = !viewsOpen)}
    on:exportData={() => (exportOpen = true)}
  />

  <div class="body">
    {#if exportError}
      <p class="error" role="alert">{exportError}</p>
    {/if}

    <Explorer on:open={open} bind:filterOpen bind:columnsOpen bind:exportOpen bind:codeOpen />
  </div>

  <!-- E11: saved-views menu is APP-level (global, works before a file opens),
       not routed through Explorer like the file-scoped dialogs. -->
  <ViewsMenu open={viewsOpen} on:close={() => (viewsOpen = false)} />
</main>

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100%;
    max-width: 100vw;
    overflow: hidden;
  }

  .body {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    /* Explorer (and its FileDrop/skeleton/error states) own their own
       internal scrolling -- DataTable's sticky header/gutter in particular
       depend on ITS `.viewport` being the nearest scrolling ancestor. `.body`
       must stay a plain bounded flex container, not a second scrollable
       layer, or the table's sticky bits and `.body`'s own scrollbar fight
       each other. */
    overflow: hidden;
  }

  .error {
    flex-shrink: 0;
    margin: 0;
    padding: var(--space-2) var(--space-4);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    font-size: 13px;
    border-bottom: 1px solid var(--border);
  }
</style>
