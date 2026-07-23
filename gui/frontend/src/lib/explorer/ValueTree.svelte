<script lang="ts">
  // Recursive expand/collapse view of ONE cell's full value (E6 Task 6). It
  // renders a value it is GIVEN (from explorer.getCell) -- it never fetches --
  // via <svelte:self> for children, like TreeNode.svelte. Objects show
  // `key: value`, arrays `[i]: value`, scalars a kind-coloured leaf reusing the
  // same KIND_TOKEN map CellView uses. A large container caps its rendered
  // children with an "N more" note so a 100k-element array cannot freeze the
  // webview.
  import { ClipboardSetText } from "../../../wailsjs/runtime";
  import { KIND_TOKEN } from "./kindToken";
  import { valueKind, isContainer, shapeChildren, childCount, scalarText } from "./valueTree";

  export let value: unknown;
  // found=false is the "the path resolved to no value" state (distinct from a
  // value that IS null). Only meaningful at the root; children are always
  // present values.
  export let found = true;
  // The label for THIS node: an object key, "[i]" for an array element, or ""
  // at the root (the root has no key -- it is just "the value").
  export let name = "";
  export let depth = 0;
  // The root renders the Copy affordance and the found=false empty state;
  // children (root=false) render neither.
  export let root = true;

  $: kind = valueKind(value);
  $: token = KIND_TOKEN[kind];
  $: color = token ? `var(--kind-${token})` : "var(--text-muted)";
  $: container = isContainer(value);
  $: count = container ? childCount(value) : 0;
  $: shaped = container ? shapeChildren(value) : { entries: [], hidden: 0, total: 0 };

  // Collapse past the first level or two: the root and its direct children
  // auto-expand, deeper containers start collapsed (the user opens them).
  let expanded = depth <= 1;
  function toggle(): void {
    expanded = !expanded;
  }

  let copied = false;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  async function copy(): Promise<void> {
    // The EXACT JSON of the value, never a .toString() of it: JSON.stringify is
    // what makes "copy this cell" round-trip into another tool.
    await ClipboardSetText(JSON.stringify(value));
    copied = true;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copied = false), 1200);
  }

  $: badge = kind === "array" ? `[${count}]` : `{${count}}`;
</script>

{#if root && !found}
  <div class="empty" role="note">no value at this path</div>
{:else}
  <div class="value-tree" class:root>
    {#if root}
      <div class="toolbar">
        <button type="button" class="copy" on:click={copy}>{copied ? "Copied" : "Copy JSON"}</button>
      </div>
    {/if}

    <div class="node" style="padding-left: {depth * 14}px;">
      {#if container}
        <button
          type="button"
          class="row caret-row"
          aria-expanded={expanded}
          on:click={toggle}
        >
          <span class="caret" aria-hidden="true">{expanded ? "▾" : "▸"}</span>
          {#if name}<span class="key">{name}</span><span class="colon">:</span>{/if}
          <span class="badge" style="color: {color};">{badge}</span>
        </button>
      {:else}
        <div class="row leaf">
          <span class="caret-spacer" aria-hidden="true"></span>
          {#if name}<span class="key">{name}</span><span class="colon">:</span>{/if}
          {#if kind === "string"}
            <span class="scalar str" style="color: {color};">{scalarText(value)}</span>
          {:else}
            <span class="scalar mono" style="color: {color};">{scalarText(value)}</span>
          {/if}
        </div>
      {/if}
    </div>

    {#if container && expanded}
      {#each shaped.entries as child (child.key)}
        <svelte:self value={child.value} name={child.key} depth={depth + 1} root={false} />
      {/each}
      {#if shaped.hidden > 0}
        <div class="more" style="padding-left: {(depth + 1) * 14}px;">{shaped.hidden} more…</div>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .value-tree.root {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text-primary);
    min-width: 0;
  }

  .toolbar {
    display: flex;
    justify-content: flex-end;
    margin-bottom: var(--space-2);
  }

  .copy {
    font-size: 11px;
    padding: 3px var(--space-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .copy:hover {
    background: color-mix(in srgb, var(--text-muted) 12%, var(--surface-2));
    color: var(--text-primary);
  }

  .copy:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 22px;
    min-width: 0;
  }

  .caret-row {
    width: 100%;
    padding: 0;
    border: none;
    background: transparent;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
    border-radius: var(--radius-sm);
  }

  .caret-row:hover {
    background: var(--surface-2);
  }

  .caret-row:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .caret,
  .caret-spacer {
    flex-shrink: 0;
    width: 12px;
    color: var(--text-muted);
    font-size: 9px;
  }

  .key {
    color: var(--text-secondary);
    white-space: nowrap;
  }

  .colon {
    color: var(--text-muted);
    margin-right: var(--space-1);
  }

  .badge {
    font-size: 11px;
    opacity: 0.85;
  }

  .scalar {
    min-width: 0;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }

  .mono {
    font-family: var(--font-mono);
  }

  .str::before,
  .str::after {
    content: '"';
    color: var(--text-muted);
  }

  .more {
    font-size: 11px;
    font-style: italic;
    color: var(--text-muted);
    min-height: 20px;
  }

  .empty {
    font-size: 12px;
    font-style: italic;
    color: var(--text-muted);
    padding: var(--space-2);
  }
</style>
