// Shared FieldDTO/Cell kind -> CSS-token map. Both FieldDTO.Types[].Kind
// (profiler-emitted, in StructureMap/KindChip) and Cell.kind (the CellKind
// enum, in CellView) carry raw profile.JSONKind values (int/float/string/
// bool/object/array/null) -- int and float collapse to the single "number"
// token because app.css has no --kind-int/--kind-float (only --kind-number).
// Any unrecognized kind -- including the literal "mixed" used for a
// drifting field's KindChip -- has no entry here and must fall back to
// --text-muted at the call site.
//
// This used to be defined twice (once in CellView.svelte, once copied here
// for KindChip.svelte); hoisted to this single module so there is exactly
// one table for both to import (T7 rule: "reuse that mapping rather than
// writing a divergent second copy").
export const KIND_TOKEN: Record<string, string> = {
  int: "number",
  float: "number",
  bool: "bool",
  string: "string",
  object: "object",
  array: "array",
  null: "null",
};
