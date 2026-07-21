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
  // E3 Task 8: when `filterActive`, formatRowCount renders the FILTERED
  // count instead of the plain file total, via a wholly separate branch --
  // every fixture above omits these four fields, so that branch is never hit
  // and the unfiltered strings stay byte-identical.
  //
  // `matchCount`/`matchExact` are CountMatches' finalized filtered result
  // (rescan/sqlite/parquet tiers -- see store.ts's startCount). On the
  // memory tier CountMatches never runs (setFilter's memory-tier skip: a
  // redundant full re-scan when page 0's own QueryRows already returns the
  // exact filtered total), so `matchCount` stays -1 there forever; in that
  // case this falls back to `total`/`totalExact`, which store.ts keeps
  // filtered-accurate on EVERY tier (memory tier's page-0 reconciliation, or
  // startCount copying its own result into `total`/`totalExact` too).
  filterActive?: boolean;
  counting?: boolean; // a CountMatches request is in flight right now
  matchCount?: number; // -1 = unknown
  matchExact?: boolean;
}

/** Formats $explorer's row count honestly, per spec §4: an exact count reads
 *  "1,234 rows"; a known-but-inexact total (sampled/rescan tier, not yet
 *  EOF-reconciled) reads "~1,234 rows"; a genuinely unknown total reads
 *  "counting…". Never presents an estimate as exact -- the `totalExact`
 *  check always gates the plain (no "~") form. */
export function formatRowCount({
  total, totalExact, rowsLoaded,
  filterActive = false, counting = false, matchCount = -1, matchExact = false,
}: RowCountInput): string {
  if (filterActive) {
    // A count in flight makes any number on hand (even a prior exact one --
    // it belonged to the PREVIOUS filter, see store.ts's setFilter) stale;
    // never render it while superseding work is running.
    if (counting) return "counting…";
    const n = matchCount >= 0 ? matchCount : total;
    const exact = matchCount >= 0 ? matchExact : totalExact;
    if (n < 0) return "counting…";
    return exact ? `${n.toLocaleString()} rows` : `~${n.toLocaleString()} rows`;
  }
  if (total < 0) return "counting…";
  if (totalExact) return `${total.toLocaleString()} rows`;
  if (total === 0 && !rowsLoaded) return "counting…";
  return `~${total.toLocaleString()} rows`;
}
