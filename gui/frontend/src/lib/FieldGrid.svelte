<script lang="ts">
  import { createEventDispatcher } from "svelte";
  import type { visual } from "../../wailsjs/go/models";
  import FieldCard from "./FieldCard.svelte";

  export let fields: visual.FieldCard[] = [];
  export let selectedPath = "";

  const dispatch = createEventDispatcher<{ select: visual.FieldCard }>();
</script>

<div class="grid">
  {#each fields as card (card.path)}
    <FieldCard
      {card}
      selected={card.path === selectedPath}
      on:select={(e) => dispatch("select", e.detail)}
    />
  {/each}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
    gap: var(--space-4);
    width: 100%;
    max-width: 100%;
  }
</style>
