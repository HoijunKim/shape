<script lang="ts">
  // E7: save a COPY of the source with the edit overlay applied (Engine.
  // SaveEdits), as JSON or NDJSON. Mirrors ExportDialog's four states (idle /
  // saving / done / failed) and focus trap. It is a copy only -- there is no
  // overwrite (the v1 review found overwrite destroyed data), and the picker
  // cannot target the open source (validateExportTarget refuses it server-side).
  import { createEventDispatcher, tick } from "svelte";
  import { explorer } from "./store";
  import { SaveFileDialog } from "../../../wailsjs/go/main/App";

  export let open = false;

  const dispatch = createEventDispatcher<{ close: void }>();

  const FORMATS: { id: string; label: string; ext: string }[] = [
    { id: "ndjson", label: "NDJSON (one object per line)", ext: "ndjson" },
    { id: "json", label: "JSON (one array)", ext: "json" },
  ];

  let format = "ndjson";
  let outPath = "";
  let pickError = "";
  let dialogEl: HTMLDivElement | undefined;
  let restoreTo: HTMLElement | null = null;

  $: if (open) void enter();

  async function enter(): Promise<void> {
    if (restoreTo) return;
    restoreTo = (document.activeElement as HTMLElement) ?? null;
    await tick();
    dialogEl?.focus();
  }

  function focusables(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(
      dialogEl.querySelectorAll<HTMLElement>("button:not([disabled]), select, input, [href], [tabindex]:not([tabindex='-1'])"),
    );
  }

  function onTab(e: KeyboardEvent): void {
    const items = focusables();
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (!active || !dialogEl?.contains(active)) { e.preventDefault(); first.focus(); return; }
    if (!e.shiftKey && active === last) { e.preventDefault(); first.focus(); }
    else if (e.shiftKey && active === first) { e.preventDefault(); last.focus(); }
  }

  $: sourceName = $explorer.path ? $explorer.path.replace(/^.*[\\/]/, "") : "data";
  $: stem = sourceName.replace(/\.[^.]+$/, "") || "data";
  $: defaultName = `${stem}-edited.${FORMATS.find((f) => f.id === format)?.ext ?? format}`;
  $: canSave = !$explorer.saving && $explorer.status === "ready" && $explorer.editedCount > 0;

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
    if (!canSave) return;
    let path = outPath;
    if (!path) {
      await choosePath();
      path = outPath;
      if (!path) return;
    }
    await explorer.saveEdits(format, path);
  }

  function close(): void {
    explorer.dismissSave();
    const back = restoreTo;
    restoreTo = null;
    dispatch("close");
    back?.focus();
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") { e.stopPropagation(); close(); return; }
    if (e.key === "Tab") onTab(e);
  }

  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    const units = ["KB", "MB", "GB", "TB"];
    let v = n / 1024, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`;
  }
</script>

<svelte:window on:keydown={open ? onKeydown : undefined} />

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <div class="backdrop" on:click={close}></div>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Save edited data" tabindex="-1" bind:this={dialogEl}>
    <div class="head">
      <span class="title">Save a copy</span>
      <button type="button" class="close" aria-label="Close" on:click={close}>✕</button>
    </div>

    {#if $explorer.saving}
      <p class="state busy" role="status">{$explorer.saveRows.toLocaleString()} rows written…</p>
    {:else if $explorer.saveResult}
      <p class="state done" role="status">
        {$explorer.saveResult.rowsOut.toLocaleString()} rows ·
        {formatBytes($explorer.saveResult.bytesOut)}
      </p>
      <p class="path mono" title={$explorer.saveResult.outPath}>{$explorer.saveResult.outPath}</p>
      <p class="counts">
        {$explorer.saveResult.editsApplied.toLocaleString()} edits applied{#if $explorer.saveResult.editsUnapplied > 0}, <span class="warn">{$explorer.saveResult.editsUnapplied.toLocaleString()} not applied</span>{/if}
      </p>
      <div class="actions"><button type="button" class="primary" on:click={close}>Done</button></div>
    {:else if $explorer.saveError}
      <p class="state failed" role="alert">{$explorer.saveError}</p>
      <div class="actions">
        <button type="button" class="primary" on:click={run}>Retry</button>
        <button type="button" on:click={close}>Close</button>
      </div>
    {:else}
      <label class="field">
        <span>Format</span>
        <select bind:value={format} aria-label="Save format">
          {#each FORMATS as f}<option value={f.id}>{f.label}</option>{/each}
        </select>
      </label>
      <label class="field">
        <span>File</span>
        <input class="mono" type="text" bind:value={outPath} placeholder={defaultName} aria-label="Output file" />
      </label>
      <p class="summary">
        Writes a COPY of the whole file with your {$explorer.editedCount} edit{$explorer.editedCount === 1 ? "" : "s"} applied, preserving nested structure and number literals. The original is untouched.
      </p>
      {#if pickError}<p class="state failed" role="alert">{pickError}</p>{/if}
      <div class="actions">
        <button type="button" on:click={choosePath}>Choose file…</button>
        <button type="button" class="primary" disabled={!canSave} on:click={run}>Save</button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.35); z-index: 20; }
  .dialog {
    position: fixed; z-index: 21; top: 50%; left: 50%; transform: translate(-50%, -50%);
    width: min(520px, 92vw); display: flex; flex-direction: column; gap: var(--space-3);
    padding: var(--space-4); background: var(--surface-1); border: 1px solid var(--border);
    border-radius: var(--radius-sm); box-sizing: border-box;
  }
  .head { display: flex; align-items: center; justify-content: space-between; }
  .title { font-weight: 600; font-size: 14px; color: var(--text-primary); }
  .close { padding: 2px var(--space-2); font-size: 12px; }
  .field { display: flex; align-items: center; gap: var(--space-3); }
  .field > span { flex: 0 0 5rem; font-size: 12px; color: var(--text-muted); }
  .field select, .field input {
    flex: 1 1 auto; min-width: 0; border: 1px solid var(--border); border-radius: var(--radius-sm);
    background: var(--surface-1); color: var(--text-primary); padding: var(--space-2) var(--space-3); font-size: 12px;
  }
  .field select:focus-visible, .field input:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .summary, .path, .counts { margin: 0; font-size: 12px; color: var(--text-muted); overflow-wrap: anywhere; }
  .counts .warn { color: var(--status-warning); }
  .state { margin: 0; font-size: 13px; }
  .state.busy { color: var(--text-muted); }
  .state.done { color: var(--text-primary); font-weight: 600; }
  .state.failed { padding: var(--space-2) var(--space-3); background: var(--status-critical-bg); color: var(--status-critical); border-radius: var(--radius-sm); }
  .actions { display: flex; justify-content: flex-end; gap: var(--space-2); }
</style>
