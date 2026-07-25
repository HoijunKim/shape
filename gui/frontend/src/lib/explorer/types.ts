// (A) EnumBind kept (wails v2.12.0 declares options.App.EnumBind).
// models.ts emits `export enum CellKind { MISSING = "missing", ... }` inside
// `namespace query`, and Cell.kind is typed as that enum. Re-export it as BOTH
// a value and a type so the renderer can compare against members. Do NOT also
// declare a string-literal union here -- comparing the enum against a bare
// "missing" is TS2367 and npm run check will reject it.
import { query } from "../../../wailsjs/go/models"; // value import, not `import type`
// E8: `visual` is used ONLY as a type here (FieldCard), so it MUST be
// `import type` -- `importsNotUsedAsValues: "error"` (from @tsconfig/svelte)
// rejects a value import that is never used as a value (TS1371). FieldDetail.
// svelte already imports it this way.
import type { visual } from "../../../wailsjs/go/models";

export type Cell = query.Cell;
export type Row = query.Row;
export type RowSet = query.RowSet;
export type Column = query.Column;
export type OpenResult = query.OpenResult;
export type ProfileDTO = query.ProfileDTO;
export type FieldDTO = query.FieldDTO;
export type CountResult = query.CountResult;
export type Filter = query.Filter;
export type Condition = query.Condition;
export type Value = query.Value;
export type CountRequest = query.CountRequest;
export type Transform = query.Transform;
export type ColumnSpec = query.ColumnSpec;
export type ExportRequest = query.ExportRequest;
export type ExportResult = query.ExportResult;
export type SaveRequest = query.SaveRequest;
export type SaveResult = query.SaveResult;
export type CellEdit = query.CellEdit;
export type ColumnStatsResult = query.ColumnStatsResult;
export type FieldCard = visual.FieldCard;
export type Generated = query.Generated;
export type CodegenRequest = query.CodegenRequest;

export const CellKind = query.CellKind;
export type CellKind = query.CellKind;
export const CELL_KINDS = Object.values(CellKind) as CellKind[];
