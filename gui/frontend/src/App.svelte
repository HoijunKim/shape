<script lang="ts">
  import { onMount } from "svelte";
  import { ProfileFile, SchemaJSON, OpenFileDialog, SaveText } from "../wailsjs/go/main/App";
  import { OnFileDrop, OnFileDropOff } from "../wailsjs/runtime/runtime";
  import type { visual } from "../wailsjs/go/models";
  import Header from "./lib/Header.svelte";
  import FileDrop from "./lib/FileDrop.svelte";
  import KpiRow from "./lib/KpiRow.svelte";
  import FieldGrid from "./lib/FieldGrid.svelte";
  import FieldDetail from "./lib/FieldDetail.svelte";

  let model: visual.VisualModel | null = null;
  let selected: visual.FieldCard | null = null;
  let loading = false;
  let error = "";
  let theme: "light" | "dark" = "light";
  // The path last successfully profiled — needed because SchemaJSON takes a
  // source file path, not the model's display name.
  let currentPath = "";

  async function load(path: string) {
    if (!path) return;
    loading = true;
    error = "";
    try {
      model = await ProfileFile(path);
      currentPath = path;
      selected = model.fields?.[0] ?? null;
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }

  async function open() {
    // OpenFileDialog resolves to "" when the user cancels; load() no-ops on
    // an empty path, so cancellation is silently ignored.
    await load(await OpenFileDialog());
  }

  async function exportSchema() {
    if (!currentPath) return;
    try {
      const schema = await SchemaJSON(currentPath);
      await SaveText("schema.json", schema);
    } catch (e) {
      error = String(e);
    }
  }

  function toggleTheme() {
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
        // SEAM for Task 8 (two-file visual diff): drop target should call
        // DiffFiles(paths[0], paths[1]) and switch to a DiffView. Until that
        // lands, just profile the first dropped file so a two-file drop
        // still does something useful rather than nothing.
        load(paths[0]);
      }
    }, true);

    return () => OnFileDropOff();
  });
</script>

<main class="app">
  <Header
    summary={model?.summary ?? null}
    canExport={!!model}
    {theme}
    on:open={open}
    on:export={exportSchema}
    on:toggleTheme={toggleTheme}
  />

  <div class="body">
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    {#if loading}
      <div class="loading">Profiling…</div>
    {:else if !model}
      <FileDrop on:open={open} />
    {:else}
      <div class="dashboard">
        <KpiRow kpis={model.kpis} />
        <div class="split">
          <div class="grid-pane">
            <FieldGrid
              fields={model.fields}
              selectedPath={selected?.path ?? ""}
              on:select={(e) => (selected = e.detail)}
            />
          </div>
          {#if selected}
            <div class="detail-pane">
              <FieldDetail card={selected} />
            </div>
          {/if}
        </div>
      </div>
    {/if}
  </div>
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
    overflow-y: auto;
    overflow-x: hidden;
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

  .loading {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
    font-size: 14px;
  }

  .dashboard {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: var(--space-5);
    min-width: 0;
    max-width: 100%;
    padding: var(--space-5);
    box-sizing: border-box;
  }

  .split {
    display: flex;
    align-items: flex-start;
    gap: var(--space-4);
    min-width: 0;
    width: 100%;
  }

  .grid-pane {
    flex: 1 1 0%;
    min-width: 0;
  }

  .detail-pane {
    flex: 0 0 380px;
    min-width: 0;
    max-width: 420px;
  }

  /* Below this width the grid and the cockpit detail stack instead of
     sitting side by side, so the page never needs to scroll horizontally. */
  @media (max-width: 900px) {
    .split {
      flex-direction: column;
    }

    .detail-pane {
      flex: 1 1 auto;
      max-width: 100%;
      width: 100%;
    }
  }
</style>
