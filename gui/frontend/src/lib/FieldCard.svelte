<script lang="ts">
  import { createEventDispatcher } from "svelte";
  import type { visual } from "../../wailsjs/go/models";
  import Sparkline from "./charts/Sparkline.svelte";
  import TypeMixBar from "./charts/TypeMixBar.svelte";
  import Meter from "./charts/Meter.svelte";
  import Badge from "./charts/Badge.svelte";

  export let card: visual.FieldCard;
  export let selected = false;

  const dispatch = createEventDispatcher<{ select: visual.FieldCard }>();

  const KNOWN_KINDS = new Set(["number", "string", "bool", "array", "object", "null"]);

  $: kindKnown = KNOWN_KINDS.has(card.kind);

  // Compact preview, chosen by form (§ P3 plan Task 5):
  //  - histogram/categorical -> the field's sparkline (shape at a glance)
  //  - typeMix -> a mini stacked type-mix bar (gate: form !== "empty", typeMix present)
  //  - meter/empty/array/highCardString -> no chart preview, just the kind chip above
  $: showSparkline =
    (card.form === "histogram" || card.form === "categorical") &&
    (card.sparkline?.length ?? 0) >= 2;
  // form === "typeMix" already excludes "empty" (they're distinct literals of the same
  // union); typeMix is additionally guarded for null since the model sends it as null
  // whenever form === "empty" (never for "typeMix", but keep the guard defensive).
  $: showTypeMix = card.form === "typeMix" && !!card.typeMix?.length;

  $: worstBadge = card.badges?.[0];
  $: showBadge = !!worstBadge && worstBadge.severity !== "good";

  function select() {
    dispatch("select", card);
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      select();
    }
  }
</script>

<div
  class="card"
  class:selected
  role="button"
  tabindex="0"
  on:click={select}
  on:keydown={onKeydown}
>
  <div class="top">
    <span class="name mono" title={card.path}>{card.displayName}</span>
    <span class="chip" class:known={kindKnown} style={kindKnown ? `--chip-color: var(--kind-${card.kind});` : ""}>
      {card.kind}
    </span>
  </div>

  {#if showSparkline}
    <div class="preview">
      <Sparkline points={card.sparkline} />
    </div>
  {:else if showTypeMix}
    <div class="preview">
      <TypeMixBar segments={card.typeMix} />
    </div>
  {/if}

  <Meter {...card.meter} />

  {#if showBadge}
    <div class="badge-slot">
      <Badge {...worstBadge} />
    </div>
  {/if}
</div>

<style>
  .card {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4);
    background: var(--surface-1);
    border-radius: var(--radius);
    border: 1px solid transparent;
    box-shadow: var(--shadow-1);
    cursor: pointer;
    max-width: 100%;
    min-width: 0;
    transition: background-color 0.15s ease, box-shadow 0.15s ease, border-color 0.15s ease,
      transform 0.15s ease;
  }

  .card:hover {
    background: var(--surface-2);
    box-shadow: 0 2px 4px rgba(11, 11, 11, 0.06), 0 8px 20px rgba(11, 11, 11, 0.1);
    transform: translateY(-1px);
  }

  .card:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .card.selected {
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent), var(--shadow-1);
  }

  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    min-width: 0;
  }

  .name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .chip {
    flex-shrink: 0;
    padding: 2px var(--space-2);
    border-radius: var(--radius-sm);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--text-muted);
    background: color-mix(in srgb, var(--text-muted) 12%, transparent);
  }

  .chip.known {
    color: var(--chip-color);
    background: color-mix(in srgb, var(--chip-color) 14%, transparent);
  }

  .preview {
    min-width: 0;
  }

  .badge-slot {
    display: flex;
  }
</style>
