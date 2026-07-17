<script lang="ts">
  // Individual props (matches the model's Badge fields relevant to rendering the pill;
  // `code`/`detail`/`path` are used by callers for tooltips/filtering, not by this component).
  export let severity = "";
  export let icon = "";
  export let label = "";

  const KNOWN_SEVERITY = new Set(["good", "warning", "serious", "critical"]);

  $: known = KNOWN_SEVERITY.has(severity);
  $: color = known ? `var(--status-${severity})` : "var(--text-secondary)";
  $: bg = known
    ? `var(--status-${severity}-bg)`
    : "color-mix(in srgb, var(--text-muted) 14%, transparent)";
</script>

<span
  class="badge"
  class:is-good={severity === "good"}
  style="color: {color}; background: {bg};"
>
  {#if icon}<span class="icon" aria-hidden="true">{icon}</span>{/if}
  {#if label}<span class="label">{label}</span>{/if}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    max-width: 100%;
    padding: 2px var(--space-2);
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-weight: 600;
    line-height: 1.4;
    white-space: nowrap;
  }

  /* The clean/no-issue state renders quieter than warning/serious/critical pills. */
  .badge.is-good {
    font-weight: 500;
    opacity: 0.85;
  }

  .icon {
    flex-shrink: 0;
  }

  .label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
