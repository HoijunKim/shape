<script lang="ts">
  // E6 Task 7: the modal that shows a cell's full value in a ValueTree. A
  // focus-trapped role="dialog" mirroring ExportDialog (Esc/backdrop close,
  // Tab kept inside, focus restored on close). Explorer owns the data: it calls
  // explorer.getCell and passes loading/error/value/found down here.
  import { createEventDispatcher, tick } from "svelte";
  import ValueTree from "./ValueTree.svelte";

  export let open = false;
  export let loading = false;
  export let error = "";
  export let value: unknown = null;
  export let found = true;
  // A human label for the value being shown (e.g. the column path), for the
  // dialog title.
  export let label = "";

  const dispatch = createEventDispatcher<{ close: void }>();

  let dialogEl: HTMLDivElement | undefined;
  let restoreTo: HTMLElement | null = null;

  $: if (open) void enter();

  async function enter(): Promise<void> {
    if (restoreTo) return; // already entered; do not re-steal focus on re-render
    restoreTo = (document.activeElement as HTMLElement) ?? null;
    await tick();
    dialogEl?.focus();
  }

  function focusables(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(
      dialogEl.querySelectorAll<HTMLElement>("button:not([disabled]), [href], [tabindex]:not([tabindex='-1'])"),
    );
  }

  function onTab(e: KeyboardEvent): void {
    const items = focusables();
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (!active || !dialogEl?.contains(active)) {
      e.preventDefault();
      first.focus();
      return;
    }
    if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    } else if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    }
  }

  function close(): void {
    const back = restoreTo;
    restoreTo = null;
    dispatch("close");
    back?.focus();
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
      return;
    }
    if (e.key === "Tab") onTab(e);
  }
</script>

<svelte:window on:keydown={open ? onKeydown : undefined} />

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <div class="backdrop" on:click={close}></div>
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-label="Cell value{label ? `: ${label}` : ''}"
    tabindex="-1"
    bind:this={dialogEl}
  >
    <div class="head">
      <span class="title">{label || "Value"}</span>
      <button type="button" class="close" aria-label="Close" on:click={close}>✕</button>
    </div>

    <div class="body">
      {#if loading}
        <p class="state busy" role="status">Loading…</p>
      {:else if error}
        <p class="state failed" role="alert">{error}</p>
      {:else}
        <ValueTree {value} {found} />
      {/if}
    </div>
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
    width: min(640px, 92vw);
    max-height: 80vh;
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
    flex-shrink: 0;
  }

  .title {
    font-weight: 600;
    font-size: 14px;
    color: var(--text-primary);
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .close {
    flex-shrink: 0;
    padding: 2px var(--space-2);
    font-size: 12px;
  }

  .body {
    min-height: 0;
    overflow: auto;
  }

  .state {
    margin: 0;
    font-size: 13px;
  }

  .state.busy {
    color: var(--text-muted);
  }

  .state.failed {
    padding: var(--space-2) var(--space-3);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    border-radius: var(--radius-sm);
  }
</style>
