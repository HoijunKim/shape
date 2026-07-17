<script lang="ts">
  import type { visual } from "../../../wailsjs/go/models";

  export let segments: visual.TypeSegment[] = [];

  const KNOWN_KINDS = new Set(["number", "string", "bool", "array", "object", "null"]);
  // Segments below this share are still drawn (and tooltip-able) in the bar,
  // just skipped in the legend to avoid clutter.
  const LEGEND_THRESHOLD = 0.06;

  function kindColor(kind: string): string {
    return KNOWN_KINDS.has(kind) ? `var(--kind-${kind})` : "var(--text-muted)";
  }

  function clampFrac(f: number): number {
    return Number.isFinite(f) ? Math.max(0, Math.min(1, f)) : 0;
  }

  $: visibleSegments = (segments ?? []).filter((seg) => seg.frac >= LEGEND_THRESHOLD);
</script>

{#if segments?.length}
  <div class="type-mix">
    <div class="bar">
      {#each segments as seg, i (i)}
        <div
          class="segment"
          style="left: {clampFrac(seg.offset) * 100}%; width: {clampFrac(seg.frac) *
            100}%; background: {kindColor(seg.kind)};"
          title="{seg.label}: {seg.percent}% ({seg.count.toLocaleString()})"
        ></div>
      {/each}
    </div>
    {#if visibleSegments.length}
      <div class="legend">
        {#each visibleSegments as seg, i (i)}
          <span class="entry" title="{seg.label}: {seg.percent}% ({seg.count.toLocaleString()})">
            <span class="swatch" style="background: {kindColor(seg.kind)};"></span>
            <span class="entry-label">{seg.label}</span>
            <span class="entry-value">{seg.percent}%</span>
          </span>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .type-mix {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    width: 100%;
    max-width: 100%;
  }

  .bar {
    position: relative;
    height: 14px;
    border-radius: 7px;
    overflow: hidden;
    background: var(--border);
  }

  .segment {
    position: absolute;
    top: 0;
    bottom: 0;
    /* 1px border on each edge (border-box sizing) reads as a 2px surface gap
       between adjacent segments without touching the offset/width math. */
    border-left: 1px solid var(--surface-1);
    border-right: 1px solid var(--surface-1);
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
    font-size: 11px;
    color: var(--text-secondary);
  }

  .entry {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    min-width: 0;
  }

  .swatch {
    flex-shrink: 0;
    width: 8px;
    height: 8px;
    border-radius: 2px;
  }

  .entry-label {
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .entry-value {
    flex-shrink: 0;
    color: var(--text-muted);
  }
</style>
