<script lang="ts">
  import type { visual } from "../../wailsjs/go/models";
  import Histogram from "./charts/Histogram.svelte";
  import CategoryBars from "./charts/CategoryBars.svelte";
  import TypeMixBar from "./charts/TypeMixBar.svelte";
  import Meter from "./charts/Meter.svelte";
  import Badge from "./charts/Badge.svelte";
  import Sparkline from "./charts/Sparkline.svelte";

  export let card: visual.FieldCard;

  const KNOWN_KINDS = new Set(["number", "string", "bool", "array", "object", "null"]);
  const KNOWN_STATUS = new Set(["good", "warning", "serious", "critical"]);

  // Hero section title per form (§ Task 6 brief). Any form not covered here
  // (incl. "meter"/"empty", and defensively any unrecognized value) falls
  // through to the Meter hero below, so this map only needs the chart forms.
  const HERO_TITLES: Record<string, string> = {
    histogram: "Distribution",
    categorical: "Category breakdown",
    highCardString: "Sample values",
    typeMix: "Type mix",
    array: "Array elements",
    meter: "Presence",
    empty: "No data",
  };

  function pct(x: number): string {
    return Number.isFinite(x) ? `${(x * 100).toFixed(1)}%` : "-";
  }

  function titleCase(s: string): string {
    return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
  }

  $: kindKnown = KNOWN_KINDS.has(card.kind);
  // card.status is the worst-severity rollup already computed by the backend
  // (visual.buildCard: card.Status = worstSeverity(card.Badges)); "" (SevNone)
  // means no badges fired, so the accent falls back to a neutral border.
  $: statusKnown = KNOWN_STATUS.has(card.status);
  $: statusColor = statusKnown ? `var(--status-${card.status})` : "var(--border)";
  $: statusLabel = statusKnown ? titleCase(card.status) : "Neutral";
  $: heroTitle = HERO_TITLES[card.form] ?? "Presence";
  $: hasSparkline = (card.sparkline?.length ?? 0) >= 2;
  $: hasBadges = (card.badges?.length ?? 0) > 0;
</script>

<aside class="detail" style="border-left-color: {statusColor};">
  <header class="head">
    <div class="title-row">
      <h2 class="name mono" title={card.path}>{card.displayName}</h2>
      <span
        class="status-dot"
        style="background: {statusColor};"
        title="Worst status: {statusLabel}"
      ></span>
    </div>
    <p class="path mono" title={card.path}>{card.path}</p>
    <div class="chips">
      <span
        class="chip"
        class:known={kindKnown}
        style={kindKnown ? `--chip-color: var(--kind-${card.kind});` : ""}
      >
        {card.kind}
      </span>
      {#if card.enumLike}<span class="chip">enum</span>{/if}
      {#if card.arrayElement}<span class="chip">array item</span>{/if}
      <span class="observations mono">{card.observations.toLocaleString()} obs</span>
    </div>
  </header>

  <section class="hero">
    <h3 class="section-title">{heroTitle}</h3>

    {#if card.form === "histogram" && card.histogram}
      <Histogram histogram={card.histogram} />
    {:else if card.form === "categorical" && card.categorical}
      <CategoryBars categorical={card.categorical} />
    {:else if card.form === "highCardString" && card.highCard}
      <div class="highcard">
        <div class="highcard-top">
          <div class="big-stat">
            <span class="big-value mono">{card.highCard.distinctText}</span>
            <span class="big-label">distinct values</span>
          </div>
          <div class="big-stat">
            <span class="big-value mono">{pct(card.highCard.uniqueRatio)}</span>
            <span class="big-label">unique ratio</span>
          </div>
        </div>
        {#if card.highCard.strLen}
          <p class="strlen mono" title="String length range">{card.highCard.strLen.text}</p>
        {/if}
        {#if card.highCard.sample?.length}
          <ul class="sample-list">
            {#each card.highCard.sample as s, i (i)}
              <li class="mono" title={s}>{s}</li>
            {/each}
          </ul>
        {/if}
      </div>
    {:else if card.form === "typeMix" && card.typeMix?.length}
      <TypeMixBar segments={card.typeMix} />
    {:else if card.form === "array" && card.array}
      <div class="array-block">
        <div class="array-meta">
          <span class="mono array-path" title={card.array.elementPath}>
            {card.array.elementPath}
          </span>
          <span class="array-count">{card.array.elementCount.toLocaleString()} elements</span>
        </div>
        {#if card.array.present && card.array.elementTypes?.length}
          <TypeMixBar segments={card.array.elementTypes} />
        {:else}
          <p class="empty-note">empty array - no elements</p>
        {/if}
      </div>
    {:else}
      <div class="hero-meter">
        <Meter {...card.meter} />
      </div>
      {#if card.form === "empty"}
        <p class="empty-note">All null - no data to visualize.</p>
      {/if}
    {/if}
  </section>

  <section class="stats-section">
    <h3 class="section-title">Stats</h3>
    {#if card.stats?.length}
      <div class="stats-grid">
        {#each card.stats as stat (stat.key)}
          <div class="stat">
            <span class="stat-label">
              {stat.label}
              {#if stat.approx}
                <span class="approx" title="Approximate (sketch-based estimate)">~</span>
              {/if}
            </span>
            <span class="stat-value mono">{stat.text}</span>
          </div>
        {/each}
      </div>
    {:else}
      <p class="empty-note">No stats for this field.</p>
    {/if}
  </section>

  <section class="meter-section">
    <h3 class="section-title">Presence &amp; nulls</h3>
    <Meter {...card.meter} />
  </section>

  <section class="badges-section">
    <h3 class="section-title">Signals</h3>
    {#if hasBadges}
      <ul class="badges-list">
        {#each card.badges as b, i (i)}
          <li class="badge-row" title={b.detail || b.label}>
            <Badge severity={b.severity} icon={b.icon} label={b.label} />
            {#if b.detail}<span class="badge-detail">{b.detail}</span>{/if}
          </li>
        {/each}
      </ul>
    {:else}
      <p class="empty-note">No signals - field looks clean.</p>
    {/if}
  </section>

  {#if hasSparkline}
    <section class="sparkline-section">
      <h3 class="section-title">Trend</h3>
      <Sparkline points={card.sparkline} />
    </section>
  {/if}
</aside>

<style>
  .detail {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    height: 100%;
    min-width: 0;
    max-width: 100%;
    overflow: auto;
    box-sizing: border-box;
    padding: var(--space-4) var(--space-5);
    background: var(--surface-1);
    border-left: 3px solid var(--border);
  }

  .head {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding-bottom: var(--space-3);
    border-bottom: 1px solid var(--border);
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }

  .name {
    margin: 0;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 18px;
    font-weight: 700;
    color: var(--text-primary);
  }

  .status-dot {
    flex-shrink: 0;
    width: 10px;
    height: 10px;
    border-radius: 50%;
  }

  .path {
    margin: 0;
    font-size: 11px;
    color: var(--text-muted);
    overflow-wrap: anywhere;
    word-break: break-all;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin-top: var(--space-1);
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

  .observations {
    margin-left: auto;
    flex-shrink: 0;
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
  }

  section {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    min-width: 0;
    max-width: 100%;
  }

  .section-title {
    margin: 0;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }

  .hero-meter {
    padding: var(--space-3);
    border-radius: var(--radius);
    background: var(--surface-2);
  }

  /* Reach into the Meter primitive to make this hero rendering read as more
     prominent than the compact instance in the always-present section below. */
  :global(.hero-meter .track) {
    height: 14px;
    border-radius: 7px;
  }

  :global(.hero-meter .labels) {
    font-size: 12px;
  }

  .empty-note {
    margin: 0;
    font-size: 12px;
    font-style: italic;
    color: var(--text-muted);
  }

  .highcard {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    min-width: 0;
  }

  .highcard-top {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-5);
  }

  .big-stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .big-value {
    font-size: 22px;
    font-weight: 700;
    color: var(--text-primary);
  }

  .big-label {
    font-size: 11px;
    color: var(--text-muted);
  }

  .strlen {
    margin: 0;
    font-size: 12px;
    color: var(--text-secondary);
  }

  .sample-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    max-height: 160px;
    overflow: auto;
  }

  .sample-list li {
    padding: 4px var(--space-2);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .array-block {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    min-width: 0;
  }

  .array-meta {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: 12px;
  }

  .array-path {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-secondary);
  }

  .array-count {
    flex-shrink: 0;
    color: var(--text-muted);
  }

  .stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
    gap: var(--space-2) var(--space-3);
  }

  .stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    padding: var(--space-2);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
  }

  .stat-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.02em;
    color: var(--text-muted);
  }

  .approx {
    margin-left: 2px;
    font-weight: 700;
    color: var(--status-warning);
  }

  .stat-value {
    font-size: 13px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-primary);
  }

  .badges-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .badge-row {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .badge-detail {
    padding-left: 2px;
    font-size: 11px;
    color: var(--text-secondary);
  }
</style>
