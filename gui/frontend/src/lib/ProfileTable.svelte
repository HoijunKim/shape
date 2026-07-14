<script lang="ts">
  import { createEventDispatcher } from "svelte";
  import type { main } from "../../wailsjs/go/models";

  export let fields: main.FieldView[] = [];
  export let selected: main.FieldView | null = null;

  const dispatch = createEventDispatcher<{ select: main.FieldView }>();

  function pct(x: number): string {
    return `${(x * 100).toFixed(1)}%`;
  }

  function dominantTypes(f: main.FieldView): string {
    const entries = Object.entries(f.typeDist ?? {}).filter(([, v]) => v > 0);
    entries.sort((a, b) => b[1] - a[1]);
    if (entries.length === 0) return "-";
    return entries
      .slice(0, 2)
      .map(([type, share]) => (entries.length > 1 ? `${type} (${pct(share)})` : type))
      .join(" / ");
  }

  function distinctLabel(f: main.FieldView): string {
    return f.distinctExact ? `${f.distinct}` : `~${f.distinct}`;
  }
</script>

<div class="table-wrap">
  <table>
    <thead>
      <tr>
        <th>Path</th>
        <th>Presence</th>
        <th>Type</th>
        <th>Null</th>
        <th>Distinct</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
      {#each fields as field (field.path)}
        <tr
          class:selected={selected?.path === field.path}
          on:click={() => dispatch("select", field)}
        >
          <td class="mono path">{field.path}</td>
          <td>{pct(field.presenceRate)}</td>
          <td class="mono">{dominantTypes(field)}</td>
          <td>{pct(field.nullRate)}</td>
          <td class="mono">{distinctLabel(field)}</td>
          <td class="badge-cell">
            {#if field.drift}<span class="drift-badge" title="Type drift detected">!</span>{/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .table-wrap {
    flex: 1 1 60%;
    overflow: auto;
    border-right: 1px solid var(--border);
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }

  thead th {
    position: sticky;
    top: 0;
    text-align: left;
    padding: 8px 12px;
    background: var(--bg-panel);
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    font-weight: 600;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }

  tbody td {
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }

  tbody tr {
    cursor: pointer;
  }

  tbody tr:hover {
    background: var(--row-hover);
  }

  tbody tr.selected {
    background: var(--row-selected);
  }

  .path {
    max-width: 28ch;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .badge-cell {
    width: 24px;
    text-align: center;
  }

  .drift-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--danger);
    color: #fff;
    font-size: 11px;
    font-weight: 700;
    line-height: 1;
  }
</style>
