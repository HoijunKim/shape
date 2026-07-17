<script lang="ts">
  import type { visual } from "../../../wailsjs/go/models";

  export let histogram: visual.Histogram;

  // Model gives us pre-rounded ints/floats for lo/hi/count; min/max are raw
  // numeric bounds we format ourselves (bin.label is already backend-formatted).
  function fmtNum(n: number): string {
    if (n === undefined || n === null || !Number.isFinite(n)) return "-";
    return Number.isInteger(n)
      ? n.toLocaleString()
      : n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  }

  function clampFrac(f: number): number {
    return Number.isFinite(f) ? Math.max(0, Math.min(1, f)) : 0;
  }

  $: bins = histogram?.bins ?? [];
</script>

{#if bins.length}
  <div class="histogram">
    <div class="plot">
      {#each bins as bin, i (i)}
        <div
          class="bar"
          style="height: {clampFrac(bin.frac) * 100}%"
          title="{bin.label}: {bin.count.toLocaleString()}"
        ></div>
      {/each}
    </div>
    <div class="axis">
      <span class="axis-label">{fmtNum(histogram.min)}</span>
      <span class="axis-label">{fmtNum(histogram.max)}</span>
    </div>
  </div>
{/if}

<style>
  .histogram {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    width: 100%;
    max-width: 100%;
  }

  .plot {
    display: flex;
    align-items: flex-end;
    gap: 2px;
    height: 96px;
    width: 100%;
  }

  .bar {
    flex: 1 1 0;
    min-width: 1px;
    background: var(--accent);
    border-radius: 4px 4px 0 0;
  }

  .axis {
    display: flex;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: 11px;
    color: var(--text-muted);
  }

  .axis-label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
