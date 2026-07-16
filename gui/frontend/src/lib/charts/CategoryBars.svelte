<script lang="ts">
  import type { visual } from "../../../wailsjs/go/models";

  export let categorical: visual.Categorical;

  function clampFrac(f: number): number {
    return Number.isFinite(f) ? Math.max(0, Math.min(1, f)) : 0;
  }

  $: bars = categorical?.bars ?? [];
  $: other = categorical?.truncated ? categorical?.other : undefined;
</script>

{#if bars.length || other}
  <div class="category-bars">
    {#each bars as bar, i (i)}
      <div class="row" title="{bar.label}: {bar.count.toLocaleString()}">
        <span class="label">{bar.label}</span>
        <div class="track">
          <div class="fill" style="width: {clampFrac(bar.frac) * 100}%"></div>
        </div>
        <span class="value">{bar.percent}%</span>
      </div>
    {/each}
    {#if other}
      <div class="row is-other" title="{other.label || 'Other'}: {other.count.toLocaleString()}">
        <span class="label">{other.label || "Other"}</span>
        <div class="track">
          <div class="fill" style="width: {clampFrac(other.frac) * 100}%"></div>
        </div>
        <span class="value">{other.percent}%</span>
      </div>
    {/if}
  </div>
{/if}

<style>
  .category-bars {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    width: 100%;
    max-width: 100%;
  }

  .row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(60px, 2fr) 3ch;
    align-items: center;
    gap: var(--space-2);
    font-size: 12px;
  }

  .label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-primary);
  }

  .track {
    height: 8px;
    border-radius: 4px;
    background: var(--border);
    overflow: hidden;
  }

  .fill {
    height: 100%;
    background: var(--accent);
    border-radius: inherit;
  }

  .value {
    text-align: right;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  /* Muted "other" bucket: quieter fill + label so it reads as the residual,
     not another ranked category. */
  .row.is-other .label {
    color: var(--text-muted);
  }

  .row.is-other .fill {
    background: var(--text-muted);
  }
</style>
