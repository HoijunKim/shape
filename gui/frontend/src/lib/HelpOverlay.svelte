<script lang="ts">
  // E12: the in-app help / onboarding overlay. An OPAQUE modal (unlike the
  // dropdowns' transparent scrim) explaining every feature, grouped into
  // sections. Opened by the header "?" button and once automatically on first
  // launch. Escape / × / backdrop close, with focus trap + restore.
  import { createEventDispatcher, tick } from "svelte";
  import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";

  function openURL(url: string) {
    BrowserOpenURL(url);
  }

  export let open = false;

  const dispatch = createEventDispatcher<{ close: void }>();

  interface Item {
    name: string;
    how: string;
    what: string;
  }
  const SECTIONS: { title: string; items: Item[] }[] = [
    {
      title: "Getting started",
      items: [
        { name: "Open a file", how: "drag a file onto the window, or the Open button", what: "JSON, NDJSON, CSV, TSV, Parquet or SQLite - big files stream, never fully loaded into memory." },
      ],
    },
    {
      title: "Explore",
      items: [
        { name: "The table", how: "scroll", what: "the real rows, virtualized - millions scroll smoothly. On huge files the Go-to-row box (top-left) jumps to any row." },
        { name: "Structure map", how: "the left sidebar", what: "every field with its type, presence and distinct count. Click a field to focus its column in the table." },
      ],
    },
    {
      title: "Shape the query",
      items: [
        { name: "Filter", how: "the Filter button (or a field's funnel icon)", what: "build type-aware AND/OR conditions by clicking - no jq or SQL. The count updates live." },
        { name: "Search", how: "the box above the table", what: "type any value; rows narrow to those where any field contains it, case-insensitive." },
        { name: "Sort", how: "a column header's ▲/▼ caret", what: "cycle none → ascending → descending. Exact over the whole result, on any file size." },
        { name: "Reshape", how: "the Columns button", what: "choose, reorder and rename the output columns." },
      ],
    },
    {
      title: "Inspect",
      items: [
        { name: "Column stats", how: "a field's chart caret in the sidebar", what: "its full profile: a distribution histogram, top values, quantiles and health flags." },
        { name: "Cell value", how: "click a truncated object/array cell", what: "the cell's whole, untruncated value as a collapsible tree, with a Copy button." },
        { name: "Row detail", how: "click a row's number", what: "the whole record as a collapsible tree - the row-level companion to the cell view." },
      ],
    },
    {
      title: "Edit & save",
      items: [
        { name: "Edit a cell", how: "double-click a scalar cell", what: "change its value in place; number literals stay exact. Edited cells highlight, and “Edited only” lists just the changes." },
        { name: "Save a copy", how: "the edit toolbar's Save button", what: "write the whole file back with your edits (JSON/NDJSON); the original is never touched." },
      ],
    },
    {
      title: "Reuse & take away",
      items: [
        { name: "Export", how: "the Export button", what: "the full result (never just the window) to JSON, NDJSON, CSV, TSV or Parquet." },
        { name: "Code", how: "the Code button", what: "the equivalent jq expression and SQL query for whatever you built, ready to copy." },
        { name: "Saved views", how: "the Views button", what: "save the current filter, search, sort and reshape under a name, and re-apply it anytime - across restarts." },
      ],
    },
  ];

  let dialogEl: HTMLDivElement | undefined;
  let restoreTo: HTMLElement | null = null;

  $: if (open) void enter();
  else restoreTo = null;

  async function enter(): Promise<void> {
    if (restoreTo) return;
    restoreTo = (document.activeElement as HTMLElement) ?? null;
    await tick();
    dialogEl?.focus();
  }

  function close(): void {
    const back = restoreTo;
    restoreTo = null;
    dispatch("close");
    back?.focus();
  }

  // Focus trap: keep Tab inside the dialog so it never escapes behind the opaque
  // backdrop to the (aria-modal-inert, invisible) header buttons -- same trap the
  // ExportDialog/SaveDialog/ValueTreeOverlay modals use.
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

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
    } else if (e.key === "Tab") {
      onTab(e);
    }
  }
</script>

<svelte:window on:keydown={open ? onKeydown : undefined} />

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="backdrop opaque" on:click={close}></div>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Help" tabindex="-1" bind:this={dialogEl}>
    <div class="head">
      <span class="title">shape - quick help</span>
      <button type="button" class="close" aria-label="Close" on:click={close}>✕</button>
    </div>
    <p class="lead">Drop in any data file and explore the real rows - filter, reshape, edit and export, no jq or SQL. Here is every feature at a glance.</p>
    <div class="body">
      {#each SECTIONS as sec (sec.title)}
        <section class="group">
          <h3>{sec.title}</h3>
          <ul>
            {#each sec.items as it (it.name)}
              <li>
                <span class="name">{it.name}</span>
                <span class="how">- {it.how}</span>
                <span class="what">{it.what}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/each}
    </div>
    <div class="foot">
      <span class="credit">shape <span class="v">v0.1.1</span> &middot; Made by <b>H.K</b> &middot;
        <button type="button" class="creditlink" on:click={() => openURL("https://github.com/hoijunkim/shape")}>GitHub</button> &middot;
        <button type="button" class="creditlink" on:click={() => openURL("https://github.com/hoijunkim/shape/blob/master/LICENSE")}>PolyForm NC 1.0.0</button>
      </span>
      <button type="button" class="primary" on:click={close}>Got it</button>
    </div>
  </div>
{/if}

<style>
  /* OPAQUE backdrop -- covers the whole app so the help is the sole focus. */
  .backdrop.opaque {
    position: fixed; inset: 0; z-index: 30;
    background: color-mix(in srgb, var(--surface-1) 92%, rgba(0, 0, 0, 0.6));
  }
  .dialog {
    position: fixed; z-index: 31; top: 50%; left: 50%; transform: translate(-50%, -50%);
    width: min(680px, 94vw); max-height: 85vh; display: flex; flex-direction: column;
    background: var(--surface-1); border: 1px solid var(--border);
    border-radius: var(--radius-sm); box-shadow: 0 12px 40px rgba(0, 0, 0, 0.3); box-sizing: border-box;
  }
  .head {
    display: flex; align-items: center; justify-content: space-between;
    padding: var(--space-3) var(--space-4); border-bottom: 1px solid var(--border);
  }
  .title { font-weight: 600; font-size: 15px; color: var(--text-primary); }
  .close { padding: 2px var(--space-2); font-size: 12px; }
  .lead {
    margin: 0; padding: var(--space-3) var(--space-4) 0;
    font-size: 12px; color: var(--text-muted);
  }
  .body { overflow-y: auto; padding: var(--space-3) var(--space-4) var(--space-4); }
  .group { margin-top: var(--space-4); }
  .group:first-child { margin-top: var(--space-2); }
  .group h3 {
    margin: 0 0 var(--space-2); font-size: 11px; font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.04em; color: var(--accent);
  }
  .group ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-2); }
  .group li { font-size: 13px; line-height: 1.45; color: var(--text-secondary); }
  .name { font-weight: 600; color: var(--text-primary); }
  .how { color: var(--text-muted); }
  .what { display: block; }
  .foot {
    display: flex; justify-content: flex-end; padding: var(--space-3) var(--space-4);
    border-top: 1px solid var(--border);
  }
  .foot { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
  .credit { font-size: 12px; color: var(--text-muted, var(--text-secondary)); }
  .credit b { color: var(--text-primary, var(--text)); }
  .credit .v { color: var(--text-muted); font-variant-numeric: tabular-nums; }
  .creditlink { background: 0; border: 0; padding: 0; font: inherit; font-size: 12px; color: var(--accent, var(--text-primary)); cursor: pointer; }
  .creditlink:hover { text-decoration: underline; }
</style>
