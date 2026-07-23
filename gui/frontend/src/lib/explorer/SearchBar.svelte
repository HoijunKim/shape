<script lang="ts">
  // E6 Task 7: the global search box. It is mounted ALWAYS in Explorer's ready
  // branch -- deliberately NOT inside FilterBar, whose whole body hides behind
  // `{#if open}` (a search you cannot see until you open the visual filter is
  // no search at all). Debounced input -> explorer.setSearch, same 250ms +
  // onDestroy-cancel discipline FilterBar established.
  import { onDestroy } from "svelte";
  import { debounce } from "./debounce";
  import { explorer } from "./store";

  // The applied search term. The box owns its own text while the file is open;
  // opening a new file remounts this component fresh (Explorer's ready branch
  // tears down on status "opening"), so no external sync is needed.
  let query = "";

  const debouncedSearch = debounce((q: string) => explorer.setSearch(q), 250);

  // Its OWN onDestroy cancel -- a second one alongside FilterBar's
  // debouncedApply teardown. Opening a second file unmounts this and mounts a
  // fresh one; a still-armed 250ms timer would otherwise fire
  // explorer.setSearch(queryForFileA) against file B's handle. progress.md
  // records this exact stale-debounce-fires-against-file-B recurrence.
  onDestroy(() => debouncedSearch.cancel());

  function onInput(e: Event): void {
    query = (e.target as HTMLInputElement).value;
    debouncedSearch.call(query);
  }

  function clear(): void {
    query = "";
    debouncedSearch.call("");
  }
</script>

<div class="search-bar">
  <span class="icon" aria-hidden="true">
    <svg viewBox="0 0 16 16" width="13" height="13" focusable="false">
      <path
        d="M6.5 1a5.5 5.5 0 0 1 4.383 8.823l3.647 3.647-1.06 1.06-3.647-3.647A5.5 5.5 0 1 1 6.5 1zm0 1.5a4 4 0 1 0 0 8 4 4 0 0 0 0-8z"
      />
    </svg>
  </span>
  <input
    type="search"
    class="search-input"
    placeholder="Search all fields…"
    aria-label="Search all fields"
    value={query}
    on:input={onInput}
  />
  {#if query}
    <button type="button" class="clear" aria-label="Clear search" title="Clear search" on:click={clear}>✕</button>
  {/if}
</div>

<style>
  .search-bar {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
    background: var(--surface-1);
    border-bottom: 1px solid var(--border);
  }

  .icon {
    display: inline-flex;
    align-items: center;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .icon svg {
    fill: currentColor;
  }

  .search-input {
    flex: 1 1 auto;
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-1);
    color: var(--text-primary);
    padding: var(--space-1) var(--space-2);
    font-size: 12px;
  }

  .search-input::placeholder {
    color: var(--text-muted);
  }

  .search-input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -1px;
  }

  .clear {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    padding: 0;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 11px;
  }

  .clear:hover {
    background: var(--surface-2);
    color: var(--text-primary);
  }

  .clear:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
