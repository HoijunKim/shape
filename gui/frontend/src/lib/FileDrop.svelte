<script lang="ts">
  import { createEventDispatcher } from "svelte";

  const dispatch = createEventDispatcher<{ open: void }>();
</script>

<div class="drop">
  <div class="drop-inner">
    <div class="brand">
      <svg class="logo" viewBox="0 0 256 256" width="44" height="44" aria-hidden="true" focusable="false">
        <rect x="0" y="0" width="256" height="256" rx="47" fill="#141413" />
        <rect x="55" y="57" width="146" height="35" rx="17.5" fill="var(--kind-number)" />
        <rect x="55" y="111" width="96" height="35" rx="17.5" fill="var(--kind-string)" />
        <rect x="55" y="165" width="58" height="35" rx="17.5" fill="var(--kind-object)" />
      </svg>
      <span class="wordmark">shape</span>
    </div>
    <p class="drop-title">Drop a data file to profile it</p>
    <button class="primary" on:click={() => dispatch("open")}>Open a file</button>
    <p class="drop-hint">Supports JSON, NDJSON, CSV, TSV, Parquet and SQLite</p>
  </div>
  <p class="credit">
    by Hoijun Kim · PolyForm Noncommercial 1.0.0 (no commercial use)
  </p>
</div>

<style>
  .drop {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 20px;
    padding: 32px;
  }

  .brand {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 12px;
    margin-bottom: 20px;
  }

  .logo {
    flex-shrink: 0;
    border-radius: 12px;
  }

  .wordmark {
    font-size: 26px;
    font-weight: 700;
    letter-spacing: -0.01em;
    color: var(--text-primary);
  }

  .credit {
    margin: 0;
    font-size: 11px;
    color: var(--text-muted);
    text-align: center;
  }

  .drop-inner {
    border: 2px dashed var(--border);
    border-radius: var(--radius);
    padding: 64px 96px;
    text-align: center;
    background: var(--surface-1);
  }

  .drop-title {
    font-size: 16px;
    margin: 0 0 16px;
    color: var(--text-primary);
  }

  .drop-hint {
    margin: 16px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
</style>
