<script lang="ts">
  // Individual props (matches the model's Meter fields 1:1) so callers can either
  // spread a `visual.Meter` (`<Meter {...card.meter} />`) or pass fields directly.
  export let presenceRate = 0;
  export let nullRate = 0;
  export let presenceText = "";
  export let nullText = "";
  export let nullStatus = "";

  const KNOWN_STATUS = new Set(["good", "warning", "serious", "critical"]);

  function statusColor(status: string): string {
    return KNOWN_STATUS.has(status) ? `var(--status-${status})` : "var(--text-muted)";
  }

  function clampPct(rate: number): number {
    if (!Number.isFinite(rate)) return 0;
    return Math.max(0, Math.min(1, rate)) * 100;
  }

  $: nullColor = statusColor(nullStatus);
  $: presencePct = clampPct(presenceRate);
  $: nullPct = clampPct(nullRate);
</script>

<div class="meter">
  <div class="track">
    <div class="fill presence" style="width: {presencePct}%"></div>
    {#if nullRate > 0}
      <div class="fill null" style="width: {nullPct}%; background: {nullColor};"></div>
    {/if}
  </div>
  {#if presenceText || nullText}
    <div class="labels">
      {#if presenceText}
        <span class="label presence-label">{presenceText}</span>
      {/if}
      {#if nullText}
        <span class="label null-label" style="color: {nullRate > 0 ? nullColor : 'var(--text-muted)'};">
          {nullText}
        </span>
      {/if}
    </div>
  {/if}
</div>

<style>
  .meter {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    max-width: 100%;
  }

  .track {
    display: flex;
    height: 8px;
    border-radius: 4px;
    background: var(--border);
    overflow: hidden;
  }

  .fill {
    height: 100%;
    flex: 0 0 auto;
  }

  .fill.presence {
    background: var(--accent);
  }

  .labels {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: 11px;
    color: var(--text-secondary);
  }

  .label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .null-label {
    flex-shrink: 0;
  }
</style>
