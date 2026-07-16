<script lang="ts">
  import type { visual } from "../../../wailsjs/go/models";

  export let points: visual.SparkPoint[] = [];

  // Screen space: viewBox 0 0 100 28. x in [0,1] -> 0..100. y in [0,1] (0 = baseline)
  // maps to 26..2 so the line has a small margin top/bottom instead of clipping.
  $: linePoints = points
    .map((p) => `${(p.x * 100).toFixed(2)},${((1 - p.y) * 24 + 2).toFixed(2)}`)
    .join(" ");
</script>

{#if points.length >= 2}
  <svg
    class="sparkline"
    viewBox="0 0 100 28"
    preserveAspectRatio="none"
    role="img"
    aria-hidden="true"
  >
    <polyline points={linePoints} />
  </svg>
{/if}

<style>
  .sparkline {
    display: block;
    width: 100%;
    max-width: 100%;
    height: 28px;
  }

  polyline {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
    vector-effect: non-scaling-stroke;
  }
</style>
