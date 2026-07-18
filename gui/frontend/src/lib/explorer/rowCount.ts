// Pure row-count formatting for the status bar (T8, spec §4's three honest
// states). Kept out of StatusBar.svelte so vitest can reach it directly --
// same rationale as widths.ts/fieldDisplay.ts in earlier tasks.
export interface RowCountInput {
  total: number; // $explorer.total; -1 = unknown
  totalExact: boolean; // $explorer.totalExact
  // Whether at least one row page has landed since the file was opened
  // (store.ts's `version > 0`). Needed because on the rescan tier `total`
  // starts life as a pre-open estimate (fileSize/avgBytes) that can floor to
  // 0 for a small file even though the file has rows -- store.ts's
  // reconcileEof only corrects that estimate once the first page actually
  // lands. Showing "No rows"/"0 rows" off that raw pre-reconciliation
  // estimate would be a real lie (obligation carried from the T8 brief), not
  // just an unpolished rough edge, so an inexact total of exactly 0 defers to
  // "counting…" until a landed page confirms it. Once a page lands, an
  // empty file's `totalExact` flips true too (see reconcileEof), so this
  // branch only ever covers a brief, genuine in-flight window -- it does not
  // change the DISPLAYED value for a resolved state, only how quickly the UI
  // is willing to trust an unconfirmed zero.
  rowsLoaded: boolean;
}

/** Formats $explorer's row count honestly, per spec §4: an exact count reads
 *  "1,234 rows"; a known-but-inexact total (sampled/rescan tier, not yet
 *  EOF-reconciled) reads "~1,234 rows"; a genuinely unknown total reads
 *  "counting…". Never presents an estimate as exact -- the `totalExact`
 *  check always gates the plain (no "~") form. */
export function formatRowCount({ total, totalExact, rowsLoaded }: RowCountInput): string {
  if (total < 0) return "counting…";
  if (totalExact) return `${total.toLocaleString()} rows`;
  if (total === 0 && !rowsLoaded) return "counting…";
  return `~${total.toLocaleString()} rows`;
}
