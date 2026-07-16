<script lang="ts">
  import type { visual } from "../../wailsjs/go/models";
  import KpiTile from "./KpiTile.svelte";

  export let kpis: visual.KPITile[] = [];
</script>

<div class="row">
  {#each kpis as tile (tile.key)}
    <div class="slot" class:hero-slot={tile.hero}>
      <KpiTile {tile} />
    </div>
  {/each}
</div>

<style>
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: stretch;
    gap: var(--space-4);
    width: 100%;
    max-width: 100%;
  }

  .slot {
    flex: 1 1 160px;
    min-width: 0;
  }

  /* The hero (health) tile gets more room to breathe than the rest of the row. */
  .slot.hero-slot {
    flex: 1.6 1 220px;
  }
</style>
