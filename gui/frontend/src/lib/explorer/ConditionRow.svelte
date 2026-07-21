<script lang="ts">
  // Task 6 (E3): one editable filter-condition row. The FIRST native form
  // controls in the app (input/select) -- app.css carries no input/select
  // tokens, so this component hand-styles them to mirror the global
  // `button` rule (app.css:145-175) rather than inventing a divergent look.
  //
  // This component is prop-controlled but SELF-EDITING: `condition` is read
  // as the initial/current value, but every handler below reassigns the
  // local `condition` binding to a new object (a plain prop reassignment,
  // which Svelte treats like any other local variable -- it re-triggers this
  // component's own $: reactivity and re-render immediately) AND dispatches
  // a `change` event carrying that same object, so a parent that owns the
  // draft list can persist it. Reassigning locally (rather than waiting for
  // the parent to feed an updated prop back down) is what makes an op
  // switch to isnull hide the value input in the SAME tick the op changes,
  // with no round trip through a parent store required.
  import { createEventDispatcher } from "svelte";
  import type { DraftCondition } from "./filterModel";
  import { conditionError } from "./filterModel";
  import { operatorsForType, defaultOpForType } from "./operators";
  import type { OpId } from "./operators";
  import type { Column } from "./types";
  import KindChip from "./KindChip.svelte";

  export let condition: DraftCondition;
  export let columns: Column[] = [];

  const dispatch = createEventDispatcher<{ change: DraftCondition; remove: { id: number } }>();

  // The comma-split list arity keeps its OWN local text buffer rather than
  // deriving the input's `value` from `condition.list.join(", ")` on every
  // keystroke: joining a freshly-split-and-trimmed array back into a string
  // would snap "a, " back to "a" mid-type (the trailing empty segment from
  // an in-progress second entry gets silently dropped), fighting the user's
  // typing. `listText` is the actual typed string; `condition.list` is just
  // its split-and-trimmed projection, kept in sync by onListInput below (and
  // reset in onColumnChange, the only other place `list` changes).
  let listText = condition.list.join(", ");

  // Defensive re-sync (review of Task 6): `listText` is a local buffer, so if
  // a parent ever swaps this row's `condition` prop for a DIFFERENT condition
  // identity (a saved-filter load, undo, or a row reused for another id) the
  // buffer would go stale against the new `condition.list`. Re-sync only on an
  // id change -- NOT on list content -- because the user's own onListInput
  // round-trips condition.list back through this prop, and re-syncing on that
  // would snap a mid-typed "a, " back to "a". FilterBar keys rows by id, so
  // this is belt-and-suspenders, not a path E3 currently exercises.
  let lastId = condition.id;
  $: if (condition.id !== lastId) {
    lastId = condition.id;
    listText = condition.list.join(", ");
  }

  // arityFor's fallback mirrors filterModel.ts's own internal arityFor: an
  // op not found in this column type's operator list (stale data) is
  // treated as arity "none" rather than throwing.
  $: ops = operatorsForType(condition.type);
  $: opSpec = ops.find((o) => o.id === condition.op);
  $: arity = opSpec ? opSpec.arity : "none";
  $: showCi = opSpec ? opSpec.ci : false;
  $: error = conditionError(condition);
  $: invalid = error !== "";
  // Non-empty entries only -- a trailing comma from an in-progress second
  // entry must not inflate the displayed chip count.
  $: listCount = condition.list.filter((x) => x !== "").length;

  function apply(next: DraftCondition): void {
    condition = next;
    dispatch("change", next);
  }

  // CRITICAL correctness rule: a column change must reset `type` to the new
  // column's type AND `op` to that type's default AND clear every value
  // field. `type` is load-bearing downstream -- buildFilter tags an
  // `in`-list's elements by `condition.type`, so a stale `type` here would
  // mis-tag a numeric column's list as strings (zero-match in the engine).
  function onColumnChange(e: Event): void {
    const path = (e.target as HTMLSelectElement).value;
    const col = columns.find((c) => c.path === path);
    const newType = col ? col.type : condition.type;
    listText = "";
    apply({
      ...condition,
      path,
      type: newType,
      op: defaultOpForType(newType),
      text: "",
      num: "",
      bool: false,
      list: [],
      ci: false,
    });
  }

  // A pure op switch (not a column switch) intentionally keeps whatever the
  // user already typed in every other field -- filterModel.ts's own
  // DraftCondition doc comment calls this out explicitly.
  function onOpChange(e: Event): void {
    apply({ ...condition, op: (e.target as HTMLSelectElement).value as OpId });
  }

  function onTextInput(e: Event): void {
    apply({ ...condition, text: (e.target as HTMLInputElement).value });
  }

  function onNumInput(e: Event): void {
    apply({ ...condition, num: (e.target as HTMLInputElement).value });
  }

  function onBoolChange(e: Event): void {
    apply({ ...condition, bool: (e.target as HTMLSelectElement).value === "true" });
  }

  function onListInput(e: Event): void {
    const raw = (e.target as HTMLInputElement).value;
    listText = raw;
    apply({ ...condition, list: raw.split(",").map((s) => s.trim()) });
  }

  function toggleCi(): void {
    apply({ ...condition, ci: !condition.ci });
  }

  function onRemove(): void {
    dispatch("remove", { id: condition.id });
  }
</script>

<div class="condition-row">
  <select aria-label="Column" value={condition.path} on:change={onColumnChange}>
    {#each columns.map((c) => c.path) as path (path)}
      <option value={path}>{path}</option>
    {/each}
  </select>

  <KindChip kind={condition.type} />

  <select aria-label="Operator" value={condition.op} on:change={onOpChange}>
    {#each ops as o (o.id)}
      <option value={o.id}>{o.label}</option>
    {/each}
  </select>

  {#if arity === "text"}
    <input
      type="text"
      class="mono"
      aria-label="Value"
      placeholder="value"
      value={condition.text}
      aria-invalid={invalid ? "true" : "false"}
      on:input={onTextInput}
    />
    {#if showCi}
      <button
        type="button"
        class="ci-toggle"
        aria-pressed={condition.ci}
        title="Case-insensitive"
        on:click={toggleCi}
      >
        Aa
      </button>
    {/if}
  {:else if arity === "number"}
    <input
      type="text"
      inputmode="decimal"
      class="mono"
      aria-label="Value"
      placeholder="number"
      value={condition.num}
      aria-invalid={invalid ? "true" : "false"}
      on:input={onNumInput}
    />
  {:else if arity === "bool"}
    <select aria-label="Value" value={condition.bool ? "true" : "false"} on:change={onBoolChange}>
      <option value="true">true</option>
      <option value="false">false</option>
    </select>
  {:else if arity === "list"}
    <input
      type="text"
      class="mono"
      aria-label="Comma-separated values"
      placeholder="a, b, c"
      value={listText}
      aria-invalid={invalid ? "true" : "false"}
      on:input={onListInput}
    />
    <span class="chip-count">{listCount} value{listCount === 1 ? "" : "s"}</span>
  {/if}

  {#if error}
    <span class="error">{error}</span>
  {/if}

  <button type="button" class="remove" aria-label="Remove condition" on:click={onRemove}>✕</button>
</div>

<style>
  .condition-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  /* No input/select tokens exist in app.css -- mirror the global `button`
     rule (app.css:145-175) so these first-ever form controls in the app
     read as part of the same system rather than browser-default chrome. */
  select,
  input {
    font-family: inherit;
    font-size: inherit;
    color: var(--text-primary);
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: var(--space-2) var(--space-3);
  }

  select:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  input {
    min-width: 0;
    flex: 1 1 auto;
  }

  .ci-toggle {
    flex-shrink: 0;
    padding: var(--space-1) var(--space-2);
    font-size: 11px;
    font-weight: 600;
  }

  .ci-toggle[aria-pressed="true"] {
    border-color: var(--accent);
    color: var(--accent);
  }

  .chip-count {
    flex-shrink: 0;
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
  }

  .error {
    flex-shrink: 0;
    font-size: 12px;
    color: var(--status-critical);
  }

  .remove {
    flex-shrink: 0;
    padding: var(--space-1) var(--space-2);
    margin-left: auto;
  }
</style>
