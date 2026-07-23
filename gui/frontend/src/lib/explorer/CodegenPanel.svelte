<script lang="ts">
  // E5: the jq/SQL panel -- the same filter and column selection the user
  // built by clicking, written out as the two languages they would otherwise
  // have had to learn first (product spec §3.6).
  //
  // Read-only on purpose: an editable box would be a query console, which is a
  // different product and would let a user execute SQL the engine never
  // validated. Copy is the only action.
  import { explorer } from "./store";
  import { ClipboardSetText } from "../../../wailsjs/runtime";

  export let open = false;

  let copied = "";
  let copyError = "";
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  async function copy(which: "jq" | "sql", text: string): Promise<void> {
    copyError = "";
    try {
      await ClipboardSetText(text);
      copied = which;
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => (copied = ""), 1500);
    } catch (e) {
      copyError = String(e);
    }
  }
</script>

{#if open}
  <div class="codegen-panel">
    <div class="head">
      <span class="title">Code</span>
      <span class="hint">the same query, as jq and SQL</span>
    </div>

    {#if $explorer.codegenError}
      <p class="error" role="alert">{$explorer.codegenError}</p>
    {/if}

    {#if $explorer.codegen}
      {#each $explorer.codegen.warnings ?? [] as w}
        <p class="warning">{w}</p>
      {/each}

      <div class="block">
        <div class="block-head">
          <span class="label">jq</span>
          <button type="button" on:click={() => copy("jq", $explorer.codegen?.jq ?? "")}>
            {copied === "jq" ? "Copied" : "Copy"}
          </button>
        </div>
        <pre class="code mono" aria-label="jq program">{$explorer.codegen.jq}</pre>
      </div>

      <div class="block">
        <div class="block-head">
          <span class="label">SQL</span>
          <button type="button" on:click={() => copy("sql", $explorer.codegen?.sql ?? "")}>
            {copied === "sql" ? "Copied" : "Copy"}
          </button>
        </div>
        <pre class="code mono" aria-label="SQL query">{$explorer.codegen.sql}</pre>
      </div>

      {#if copyError}
        <p class="error" role="alert">{copyError}</p>
      {/if}
    {/if}
  </div>
{/if}

<style>
  /* Same face as the filter and columns panels, and its own scroll: three
     docked panels at 45vh each would otherwise push the table off-screen. */
  .codegen-panel {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    background: var(--surface-1);
    border-top: 1px solid var(--border);
    box-sizing: border-box;
  }

  .head {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
  }

  .title {
    font-weight: 600;
    font-size: 13px;
    color: var(--text-primary);
  }

  .hint {
    font-size: 12px;
    color: var(--text-muted);
  }

  .block {
    display: flex;
    flex-direction: column;
    gap: var(--space-1, 4px);
  }

  .block-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .label {
    font-size: 12px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .code {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--text-primary);
    font-size: 12px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .warning {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    background: var(--status-warn-bg, var(--surface-2));
    color: var(--status-warn, var(--text-primary));
    font-size: 12px;
    border-radius: var(--radius-sm);
  }

  .error {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    background: var(--status-critical-bg);
    color: var(--status-critical);
    font-size: 12px;
    border-radius: var(--radius-sm);
  }
</style>
