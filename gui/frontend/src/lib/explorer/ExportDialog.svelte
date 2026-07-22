<script lang="ts">
  // E4 Task 10: the export dialog -- pick a format, pick a file, write the
  // CURRENT filter + column selection out in full (product spec §3.6).
  //
  // Four states, all reachable and all tested: idle, exporting (live row count
  // + Cancel), done (rows/bytes/path + any fidelity warnings), failed (the
  // error + Retry). A cancelled export lands in `failed` saying "cancelled" --
  // never a silent close, which would look identical to a finished export.
  import { createEventDispatcher } from "svelte";
  import { explorer } from "./store";
  import { SaveFileDialog } from "../../../wailsjs/go/main/App";

  export let open = false;
  // Non-empty when the columns panel is in an invalid state (duplicate or
  // blank names). Advisory only: ExportQuery validates the same rules
  // server-side, which is what actually makes a bad export impossible.
  export let disabledReason = "";

  const dispatch = createEventDispatcher<{ close: void }>();

  const FORMATS: { id: string; label: string; ext: string }[] = [
    { id: "ndjson", label: "NDJSON (one object per line)", ext: "ndjson" },
    { id: "json", label: "JSON (one array)", ext: "json" },
    { id: "csv", label: "CSV", ext: "csv" },
    { id: "tsv", label: "TSV", ext: "tsv" },
    { id: "parquet", label: "Parquet", ext: "parquet" },
  ];

  let format = "ndjson";
  let outPath = "";
  let pickError = "";

  $: sourceName = $explorer.path ? $explorer.path.replace(/^.*[\\/]/, "") : "data";
  $: stem = sourceName.replace(/\.[^.]+$/, "") || "data";
  $: defaultName = `${stem}-export.${FORMATS.find((f) => f.id === format)?.ext ?? format}`;
  $: rowSummary =
    $explorer.filterActive && $explorer.matchCount >= 0
      ? `${$explorer.matchCount.toLocaleString()} matching rows`
      : $explorer.total >= 0
        ? `${$explorer.totalExact ? "" : "~"}${$explorer.total.toLocaleString()} rows`
        : "all matching rows";
  $: canExport = !$explorer.exporting && disabledReason === "" && $explorer.status === "ready";

  async function choosePath(): Promise<void> {
    pickError = "";
    try {
      const chosen = await SaveFileDialog(defaultName, format);
      if (chosen) outPath = chosen;
    } catch (e) {
      pickError = String(e);
    }
  }

  async function run(): Promise<void> {
    if (!canExport) return;
    let path = outPath;
    if (!path) {
      await choosePath();
      path = outPath;
      if (!path) return; // the picker was cancelled
    }
    await explorer.runExport(format, path);
  }

  function close(): void {
    // Esc/backdrop during an export CANCELS it rather than closing behind the
    // user's back -- an export left running with its dialog gone has no way to
    // report where the file went, or that it failed.
    if ($explorer.exporting) {
      explorer.cancelExport();
      return;
    }
    explorer.dismissExport();
    dispatch("close");
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
    }
  }

  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    const units = ["KB", "MB", "GB", "TB"];
    let v = n / 1024;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`;
  }
</script>

<svelte:window on:keydown={open ? onKeydown : undefined} />

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <div class="backdrop" on:click={close}></div>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Export data">
    <div class="head">
      <span class="title">Export</span>
      <button type="button" class="close" aria-label="Close" on:click={close}>✕</button>
    </div>

    {#if $explorer.exporting}
      <p class="state busy" role="status">
        {$explorer.exportRows.toLocaleString()} rows written…
      </p>
      <div class="actions">
        <button type="button" on:click={() => explorer.cancelExport()}>Cancel</button>
      </div>
    {:else if $explorer.exportResult}
      <p class="state done" role="status">
        {$explorer.exportResult.rowsOut.toLocaleString()} rows ·
        {formatBytes($explorer.exportResult.bytesOut)}
      </p>
      <p class="path mono" title={$explorer.exportResult.outPath}>{$explorer.exportResult.outPath}</p>
      {#if $explorer.exportResult.warnings && $explorer.exportResult.warnings.length > 0}
        {#each $explorer.exportResult.warnings as w}
          <p class="warning">{w}</p>
        {/each}
      {/if}
      <div class="actions">
        <button type="button" class="primary" on:click={close}>Done</button>
      </div>
    {:else if $explorer.exportError}
      <p class="state failed" role="alert">{$explorer.exportError}</p>
      <div class="actions">
        <button type="button" class="primary" on:click={run}>Retry</button>
        <button type="button" on:click={close}>Close</button>
      </div>
    {:else}
      <label class="field">
        <span>Format</span>
        <select bind:value={format} aria-label="Export format">
          {#each FORMATS as f}
            <option value={f.id}>{f.label}</option>
          {/each}
        </select>
      </label>

      <label class="field">
        <span>File</span>
        <input
          class="mono"
          type="text"
          bind:value={outPath}
          placeholder={defaultName}
          aria-label="Output file"
        />
      </label>

      <p class="summary">
        Exports the current filter and column selection — {rowSummary}, {$explorer.columns.length}
        column{$explorer.columns.length === 1 ? "" : "s"}.
      </p>

      {#if pickError}<p class="state failed" role="alert">{pickError}</p>{/if}
      {#if disabledReason}<p class="state failed" role="alert">{disabledReason}</p>{/if}

      <div class="actions">
        <button type="button" on:click={choosePath}>Choose file…</button>
        <button type="button" class="primary" disabled={!canExport} on:click={run}>Export</button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.35);
    z-index: 20;
  }

  .dialog {
    position: fixed;
    z-index: 21;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(520px, 92vw);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4);
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    box-sizing: border-box;
  }

  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .title {
    font-weight: 600;
    font-size: 14px;
    color: var(--text-primary);
  }

  .close {
    padding: 2px var(--space-2);
    font-size: 12px;
  }

  .field {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .field > span {
    flex: 0 0 5rem;
    font-size: 12px;
    color: var(--text-muted);
  }

  .field select,
  .field input {
    flex: 1 1 auto;
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-1);
    color: var(--text-primary);
    padding: var(--space-2) var(--space-3);
    font-size: 12px;
  }

  .field select:focus-visible,
  .field input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .summary,
  .path {
    margin: 0;
    font-size: 12px;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }

  .state {
    margin: 0;
    font-size: 13px;
  }

  .state.busy {
    color: var(--text-muted);
  }

  .state.done {
    color: var(--text-primary);
    font-weight: 600;
  }

  .state.failed {
    padding: var(--space-2) var(--space-3);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    border-radius: var(--radius-sm);
  }

  .warning {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    background: var(--status-warn-bg, var(--surface-2));
    color: var(--status-warn, var(--text-primary));
    font-size: 12px;
    border-radius: var(--radius-sm);
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }
</style>
