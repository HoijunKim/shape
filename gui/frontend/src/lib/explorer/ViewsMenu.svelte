<script lang="ts">
  // E11: the saved-views dropdown. Save the current query shape (filter + search
  // + sort + reshape) under a name, and apply/delete saved views. Views are
  // global (loaded once at store init, persisted to a config file), so this menu
  // is mounted at App level and works before any file is open -- though Save is
  // disabled until a file is open (a view of nothing is meaningless).
  import { createEventDispatcher, tick } from "svelte";
  import { explorer } from "./store";

  export let open = false;

  const dispatch = createEventDispatcher<{ close: void }>();

  let name = "";
  let menuEl: HTMLDivElement | undefined;
  let restoreTo: HTMLElement | null = null;

  $: if (open) void enter();

  async function enter(): Promise<void> {
    if (restoreTo) return;
    restoreTo = (document.activeElement as HTMLElement) ?? null;
    await tick();
    (menuEl?.querySelector("input") as HTMLInputElement | null)?.focus();
  }

  $: canSave = name.trim() !== "" && $explorer.status === "ready";

  function save(): void {
    if (!canSave) return;
    explorer.saveView(name.trim());
    name = "";
  }

  function apply(viewName: string): void {
    explorer.applyView(viewName);
    close();
  }

  function remove(viewName: string): void {
    explorer.deleteView(viewName);
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
    }
  }
</script>

<svelte:window on:keydown={open ? onKeydown : undefined} />

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="backdrop" on:click={close}></div>
  <div class="menu" role="dialog" aria-modal="true" aria-label="Saved views" bind:this={menuEl}>
    <div class="head">
      <span class="title">Saved views</span>
      <button type="button" class="close" aria-label="Close" on:click={close}>✕</button>
    </div>

    <form class="save-row" on:submit|preventDefault={save}>
      <input
        type="text"
        bind:value={name}
        placeholder="Save current view as…"
        aria-label="View name"
      />
      <button type="button" class="primary" disabled={!canSave} on:click={save}>Save</button>
    </form>

    {#if $explorer.views.length === 0}
      <p class="empty">No saved views yet.</p>
    {:else}
      <ul class="views">
        {#each $explorer.views as v (v.name)}
          <li class="view-row">
            <button type="button" class="apply" title="Apply this view" on:click={() => apply(v.name)}>{v.name}</button>
            <button type="button" class="del" aria-label="Delete {v.name}" title="Delete" on:click={() => remove(v.name)}>✕</button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; background: transparent; z-index: 20; }
  .menu {
    position: fixed; z-index: 21; top: 48px; right: var(--space-3);
    width: min(300px, 92vw); display: flex; flex-direction: column; gap: var(--space-2);
    padding: var(--space-3); background: var(--surface-1); border: 1px solid var(--border);
    border-radius: var(--radius-sm); box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18); box-sizing: border-box;
  }
  .head { display: flex; align-items: center; justify-content: space-between; }
  .title { font-weight: 600; font-size: 13px; color: var(--text-primary); }
  .close { padding: 2px var(--space-2); font-size: 12px; }
  .save-row { display: flex; gap: var(--space-2); }
  .save-row input {
    flex: 1 1 auto; min-width: 0; border: 1px solid var(--border); border-radius: var(--radius-sm);
    background: var(--surface-1); color: var(--text-primary); padding: var(--space-2); font-size: 12px;
  }
  .save-row input:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .save-row .primary { flex-shrink: 0; padding: 2px var(--space-3); font-size: 12px; }
  .empty { margin: 0; font-size: 12px; color: var(--text-muted); }
  .views { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .view-row { display: flex; align-items: center; gap: var(--space-2); }
  .apply {
    flex: 1 1 auto; min-width: 0; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    padding: var(--space-2); font-size: 12px; border: none; background: transparent; color: var(--text-primary);
    border-radius: var(--radius-sm); cursor: pointer;
  }
  .apply:hover { background: var(--surface-2); }
  .del { flex-shrink: 0; padding: 2px var(--space-2); font-size: 11px; color: var(--text-muted); }
  .del:hover { color: var(--status-critical); }
</style>
