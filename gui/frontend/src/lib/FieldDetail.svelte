<script lang="ts">
  import type { main } from "../../wailsjs/go/models";

  export let field: main.FieldView;

  function pct(x: number): string {
    return `${(x * 100).toFixed(1)}%`;
  }

  $: typeEntries = Object.entries(field.typeDist ?? {})
    .filter(([, v]) => v > 0)
    .sort((a, b) => b[1] - a[1]);

  $: hasNumericRange = field.min !== undefined || field.max !== undefined;
  $: hasStrLenRange = field.strLenMin !== undefined || field.strLenMax !== undefined;
</script>

<aside class="detail">
  <h2 class="mono path" title={field.path}>{field.path}</h2>
  {#if field.drift}<p class="drift-note">Type drift detected across records</p>{/if}

  <section>
    <h3>Type distribution</h3>
    <div class="bars">
      {#each typeEntries as [type, share] (type)}
        <div class="bar-row">
          <span class="bar-label mono">{type}</span>
          <div class="bar-track">
            <div class="bar-fill" style="width: {share * 100}%"></div>
          </div>
          <span class="bar-value">{pct(share)}</span>
        </div>
      {/each}
    </div>
  </section>

  <section class="stats">
    <div class="stat">
      <span class="stat-label">Null rate</span>
      <span class="stat-value">{pct(field.nullRate)}</span>
    </div>
    <div class="stat">
      <span class="stat-label">Distinct</span>
      <span class="stat-value mono">
        {field.distinctExact ? field.distinct : `~${field.distinct}`}
      </span>
    </div>
    {#if hasNumericRange}
      <div class="stat">
        <span class="stat-label">Min</span>
        <span class="stat-value mono">{field.min ?? "-"}</span>
      </div>
      <div class="stat">
        <span class="stat-label">Max</span>
        <span class="stat-value mono">{field.max ?? "-"}</span>
      </div>
    {/if}
    {#if hasStrLenRange}
      <div class="stat">
        <span class="stat-label">Str len min</span>
        <span class="stat-value mono">{field.strLenMin ?? "-"}</span>
      </div>
      <div class="stat">
        <span class="stat-label">Str len max</span>
        <span class="stat-value mono">{field.strLenMax ?? "-"}</span>
      </div>
    {/if}
  </section>

  {#if field.topValues?.length}
    <section>
      <h3>Top values</h3>
      <ul class="top-values">
        {#each field.topValues as v (v.value)}
          <li>
            <span class="mono value">{v.value}</span>
            <span class="count">{v.count.toLocaleString()}</span>
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</aside>

<style>
  .detail {
    flex: 1 1 40%;
    overflow: auto;
    padding: 16px 20px;
    background: var(--bg-panel);
  }

  h2.path {
    margin: 0 0 4px;
    font-size: 15px;
    word-break: break-all;
  }

  .drift-note {
    margin: 0 0 12px;
    color: var(--danger);
    font-size: 12px;
    font-weight: 600;
  }

  h3 {
    margin: 20px 0 8px;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    color: var(--text-muted);
  }

  section:first-of-type h3 {
    margin-top: 8px;
  }

  .bars {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .bar-row {
    display: grid;
    grid-template-columns: 8ch 1fr 6ch;
    align-items: center;
    gap: 8px;
    font-size: 12px;
  }

  .bar-label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .bar-track {
    height: 8px;
    border-radius: 4px;
    background: var(--border);
    overflow: hidden;
  }

  .bar-fill {
    height: 100%;
    background: var(--accent);
  }

  .bar-value {
    text-align: right;
    color: var(--text-muted);
  }

  .stats {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px 16px;
  }

  .stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .stat-label {
    font-size: 11px;
    color: var(--text-muted);
  }

  .stat-value {
    font-size: 13px;
  }

  .top-values {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .top-values li {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    padding: 4px 8px;
    border-radius: var(--radius);
    background: var(--bg-elevated);
    font-size: 12px;
  }

  .value {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .count {
    color: var(--text-muted);
    flex-shrink: 0;
  }
</style>
