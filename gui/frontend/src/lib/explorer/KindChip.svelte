<script lang="ts">
  // Small pill showing a field's dominant kind (or the literal "mixed" for a
  // drifting field -- see fieldDisplay.ts's dominantKind). Mirrors the
  // kind-chip color guard in FieldCard.svelte:14-16: an unrecognized kind
  // (including "mixed" itself, which has no --kind-mixed token) renders in
  // --text-muted instead of a kind color.
  import { KIND_TOKEN } from "./kindToken";

  export let kind = "";

  $: known = kind in KIND_TOKEN;
  $: token = KIND_TOKEN[kind];
</script>

<span class="kind-chip" class:known style={known ? `--chip-color: var(--kind-${token});` : ""}>
  {kind}
</span>

<style>
  .kind-chip {
    flex-shrink: 0;
    padding: 1px var(--space-2);
    border-radius: var(--radius-sm);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--text-muted);
    background: color-mix(in srgb, var(--text-muted) 12%, transparent);
  }

  .kind-chip.known {
    color: var(--chip-color);
    background: color-mix(in srgb, var(--chip-color) 14%, transparent);
  }
</style>
