<script lang="ts">
  import type { visual } from "../../wailsjs/go/models";

  export let tile: visual.KPITile;

  const KNOWN_STATUS = new Set(["good", "warning", "serious", "critical"]);

  $: known = !!tile.severity && KNOWN_STATUS.has(tile.severity);
  $: accent = known ? `var(--status-${tile.severity})` : "";
</script>

<div
  class="tile"
  class:hero={tile.hero}
  class:accented={known}
  style={known ? `--tile-accent: ${accent};` : ""}
>
  <span class="label">{tile.label}</span>
  <span class="value">{tile.value}</span>
  {#if tile.sub}
    <span class="sub" style={known ? `color: ${accent};` : ""}>{tile.sub}</span>
  {/if}
</div>

<style>
  .tile {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    min-width: 140px;
    padding: var(--space-4) var(--space-5);
    background: var(--surface-1);
    border-radius: var(--radius);
    box-shadow: var(--shadow-1);
    max-width: 100%;
  }

  .tile.accented {
    border-left: 3px solid var(--tile-accent);
    padding-left: calc(var(--space-5) - 3px);
  }

  .label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }

  .value {
    font-family: var(--font-mono);
    font-size: 26px;
    font-weight: 600;
    line-height: 1.2;
    color: var(--text-primary);
  }

  .sub {
    font-size: 12px;
    color: var(--text-secondary);
  }

  /* Hero (health) tile: a step up in scale plus a soft tint wash from its
     severity, so the single most important KPI reads first without ever
     relying on color alone (label/value/sub text still carry the meaning). */
  .tile.hero {
    padding: var(--space-5) var(--space-6);
  }

  .tile.hero .value {
    font-size: 36px;
  }

  .tile.hero.accented {
    background: linear-gradient(
      180deg,
      color-mix(in srgb, var(--tile-accent) 10%, var(--surface-1)),
      var(--surface-1)
    );
  }
</style>
