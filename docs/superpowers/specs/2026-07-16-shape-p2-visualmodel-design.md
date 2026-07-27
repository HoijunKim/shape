# Shape P2 - internal/visual VisualModel Design (detail spec)

Date: 2026-07-16
Status: Approved for planning
Author: hoijun (with Claude; judge-panel synthesis of 3 independent designs)

This is the detail spec for P2 of the visual-dashboard effort (parent spec:
`2026-07-15-shape-visual-dashboard-design.md`, §3/§4/§5/§6). It defines the new
`internal/visual` package: pure-data chart geometry (a "VisualModel") computed
once in Go and consumed by both the Svelte GUI (P3) and the Go html/template
HTML report (P6). Package is stdlib-only, deterministic, golden-testable.

## Verification & refinements (checked against the current codebase)

Confirmed the referenced APIs exist as used:
- `diff.DiffResult{Old,New,Compared,Added,Removed,Changed,Breaking,Caveats,Changes}`,
  `func (d DiffResult) HasBreaking() bool`, `Change{Path,Kind,Breaking,Details}`,
  `Detail{Reason Reason,Breaking,Message,Old,New}`.
- `ChangeKind`: `Added="added"`, `Removed="removed"`, `Changed="changed"`.
- `Reason`: `ReasonPresence="presence"`, `ReasonType="type"`, `ReasonEnum="enum"`.
- `diff.pct` is UNEXPORTED, so `visual` defines its own `fmtPct` with the identical
  formula `strconv.Itoa(int(f*100+0.5)) + "%"`.
- `readers.DetectFormat(path, formatFlag, peek)` exists but needs peek bytes and
  returns a `Format` enum; `visual` therefore uses its OWN extension→label switch
  (§7) rather than importing it.

**Refinement to §6.1 `type_narrowing` detection:** do NOT string-parse the diff
message. Detect narrowing structurally from the `Detail` where `Reason==ReasonType`
and `Breaking`: split `Detail.Old` and `Detail.New` on `,` into type-token sets;
narrowing = `New ⊊ Old` (New is a strict subset of Old, i.e. every New token is in
Old and `len(New) < len(Old)`). This is robust and does not depend on message text.

---

## Package API

```go
package visual

import (
	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/hoijun-kim/shape/internal/profile"
)

// Options carries render metadata that ProfileResult does not itself hold.
// ProfileResult.Source is only set on the diff path, and no detected format is
// stored anywhere, so the caller passes them here. This is the one deliberate
// extension to the spec's FromProfile(ProfileResult) signature.
type Options struct {
	Name   string // display name (filename/label); "" -> ProfileResult.Source
	Format string // "JSON"|"NDJSON"|"CSV"|"TSV"|"Parquet"|"SQLite"; "" -> derive from Name/Source ext
}

// FromProfile builds the whole-file dashboard model.
func FromProfile(res profile.ProfileResult, opts Options) VisualModel

// FromDiff builds the two-file comparison model.
func FromDiff(d diff.DiffResult) DiffVisualModel
```

**Determinism contract (must hold for golden tests):**
- One `FieldCard` per `profile.FieldProfile`, in `ProfileResult.Fields` order
  (already path-sorted).
- No Go map is ever iterated into output. `TypeDist` is always projected through
  the fixed `kindOrder` slice; `TopValues` are already `(count desc, value asc)`.
- Each card is a pure function of its `FieldProfile` plus a read-only
  `map[string]FieldProfile` index (used only by array containers to reach their
  `path+"[]"` element). One whole-model pass then computes `Summary` aggregates.
- All rounding/formatting happens once, here. Fractions drive geometry;
  preformatted text drives labels. Views do `fraction × extent` and print text.

## 1. Shared value types

```go
type Severity string

const (
	SevCritical Severity = "critical"
	SevSerious  Severity = "serious"
	SevWarning  Severity = "warning"
	SevGood     Severity = "good"
	SevNone     Severity = "" // neutral track / use series color
)

// severityRank orders severities for "worst wins" reductions and sorting.
var severityRank = map[Severity]int{
	SevNone: 0, SevGood: 1, SevWarning: 2, SevSerious: 3, SevCritical: 4,
}

// severityIcon is the frozen glyph table (golden-pinned; renderer may remap).
var severityIcon = map[Severity]string{
	SevCritical: "⛔", SevSerious: "❗", SevWarning: "⚠", SevGood: "✓",
}

// kindOrder fixes display order & palette slot of every non-null kind. int+float
// fold to "number" (matching profile.IsTypeDrift). Index == TypeSegment.Series.
var kindOrder = []struct {
	Kind  string
	Label string
}{
	{"number", "Number"}, {"string", "String"}, {"bool", "Boolean"},
	{"array", "Array"}, {"object", "Object"}, {"null", "Null"},
}

type Badge struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Icon     string   `json:"icon"`
	Label    string   `json:"label"`
	Detail   string   `json:"detail"`
	Path     string   `json:"path,omitempty"`
}

type Meter struct {
	PresenceRate float64  `json:"presenceRate"`
	NullRate     float64  `json:"nullRate"`
	PresenceText string   `json:"presenceText"`
	NullText     string   `json:"nullText"`
	NullStatus   Severity `json:"nullStatus,omitempty"`
}

type TypeSegment struct {
	Kind    string  `json:"kind"`
	Label   string  `json:"label"`
	Frac    float64 `json:"frac"`
	Offset  float64 `json:"offset"`
	Count   int     `json:"count"`
	Percent int     `json:"percent"`
	Series  int     `json:"series"`
}

type Stat struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Text   string `json:"text"`
	Approx bool   `json:"approx,omitempty"`
}
```

## 2. Whole-file model, KPI tiles, FieldCard

```go
type VisualModel struct {
	Summary Summary     `json:"summary"`
	KPIs    []KPITile   `json:"kpis"`
	Fields  []FieldCard `json:"fields"`
	Badges  []Badge     `json:"badges"`
}

type Summary struct {
	Name           string   `json:"name"`
	Format         string   `json:"format"`
	Records        int      `json:"records"`
	Skipped        int      `json:"skipped"`
	FieldCount     int      `json:"fieldCount"`
	WarningCount   int      `json:"warningCount"`
	HealthScore    int      `json:"healthScore"`
	HealthGrade    string   `json:"healthGrade"`
	HealthSeverity Severity `json:"healthSeverity"`
}

type KPITile struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Value    string   `json:"value"`
	Raw      float64  `json:"raw"`
	Sub      string   `json:"sub,omitempty"`
	Severity Severity `json:"severity,omitempty"`
	Hero     bool     `json:"hero,omitempty"`
}

type FieldCard struct {
	Path         string    `json:"path"`
	DisplayName  string    `json:"displayName"`
	Form         ChartForm `json:"form"`
	Kind         string    `json:"kind"`
	EnumLike     bool      `json:"enumLike"`
	ArrayElement bool      `json:"arrayElement"`
	Observations int       `json:"observations"`
	Status       Severity  `json:"status"`

	TypeMix []TypeSegment `json:"typeMix"`
	Meter   Meter         `json:"meter"`
	Stats   []Stat        `json:"stats"`
	Badges  []Badge       `json:"badges"`

	Histogram   *Histogram      `json:"histogram,omitempty"`
	Categorical *Categorical    `json:"categorical,omitempty"`
	HighCard    *HighCardString `json:"highCard,omitempty"`
	Array       *ArrayBreakdown `json:"array,omitempty"`
	Sparkline   []SparkPoint    `json:"sparkline,omitempty"`
}

type ChartForm string

const (
	FormHistogram   ChartForm = "histogram"
	FormCategorical ChartForm = "categorical"
	FormHighCard    ChartForm = "highCardString"
	FormTypeMix     ChartForm = "typeMix"
	FormArray       ChartForm = "array"
	FormMeter       ChartForm = "meter"
	FormEmpty       ChartForm = "empty"
)

type Histogram struct {
	Min      float64   `json:"min"`
	Max      float64   `json:"max"`
	BinWidth float64   `json:"binWidth"`
	Bins     []HistBar `json:"bins"`
	MaxCount int       `json:"maxCount"`
	Total    int       `json:"total"`
}

type HistBar struct {
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Count int     `json:"count"`
	Frac  float64 `json:"frac"`
	Label string  `json:"label"`
}

type Categorical struct {
	Bars      []CategoryBar `json:"bars"`
	Other     *CategoryBar  `json:"other,omitempty"`
	Total     int           `json:"total"`
	MaxCount  int           `json:"maxCount"`
	Truncated bool          `json:"truncated"`
}

type CategoryBar struct {
	Label   string  `json:"label"`
	Count   int     `json:"count"`
	Frac    float64 `json:"frac"`
	Percent int     `json:"percent"`
}

type HighCardString struct {
	Distinct     int        `json:"distinct"`
	DistinctText string     `json:"distinctText"`
	UniqueRatio  float64    `json:"uniqueRatio"`
	Sample       []string   `json:"sample"`
	StrLen       *StrLenBar `json:"strLen,omitempty"`
}

type StrLenBar struct {
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	Text string `json:"text"`
}

type ArrayBreakdown struct {
	ElementPath  string        `json:"elementPath"`
	Present      bool          `json:"present"`
	ElementCount int           `json:"elementCount"`
	ElementTypes []TypeSegment `json:"elementTypes"`
}

type SparkPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
```

### 2.1 KPI row (fixed order)

| Key | Label | Value | Sub | Severity |
|---|---|---|---|---|
| `records` | Records | `fmtInt(Records)` | `fmtInt(Skipped)+" skipped"` if `Skipped>0` | `SevWarning` if `Skipped>0` |
| `fields` | Fields | `fmtInt(FieldCount)` | - | - |
| `format` | Format | `Format` | - | - |
| `warnings` | Warnings | `fmtInt(WarningCount)` | - | `SevGood` if 0, else worst card `Status` |
| `health` | Health | `Itoa(HealthScore)` | `HealthGrade` | `HealthSeverity`, `Hero:true` |

`WarningCount` = number of `FieldCard`s whose `Status` rank `>= SevWarning`.
`DisplayName` = last `.`-segment of `Path` (root `"$"` stays `"$"`; trailing `[]`
kept, e.g. `user.tags[]` -> `tags[]`).

## 3. Chart-form selection (concrete named-const thresholds)

```go
const (
	DisplayBins            = 20
	CategoricalMaxDistinct = 25
	DiscreteNumericMax     = 12
	EnumMinDistinct        = 2
	EnumMaxDistinct        = 10
	TopK                   = 10
	CardinalitySample      = 5
	NullWarnBand           = 0.20
	NullSeriousBand        = 0.50
)
```

Per field: `drift = profile.IsTypeDrift(fp)`; `dom` = the single non-null folded
kind (`""` if all-null / no obs). **First match wins:**

```
selectForm(fp):
  1. fp.Observations == 0  OR  fp.NullRate >= 1.0        -> FormEmpty     (Kind "empty")
  2. drift                                               -> FormTypeMix   (Kind "mixed")
  3. switch dom:                                         // exactly one non-null kind
       "number":
           if fp.DistinctExact && 1 <= fp.DistinctCount <= DiscreteNumericMax
                                                         -> FormCategorical (Kind "number")
           else if len(fp.Histogram) > 0                -> FormHistogram   (Kind "number")
           else                                          -> FormMeter       (Kind "number")
       "string":
           if fp.DistinctExact && 1 <= fp.DistinctCount <= CategoricalMaxDistinct
                                                         -> FormCategorical (Kind "string")
           else                                          -> FormHighCard    (Kind "string")
       "array"                                            -> FormArray       (Kind "array")
       "bool", "object"                                   -> FormMeter       (Kind dom)
```

Rationale: multi-type via `profile.IsTypeDrift` (folds int/float, ignores null);
discrete numerics populate `TopValues` so they render as bars; bool is counted
only in `kindCounts` (no true/false split recoverable) -> meter; array container
uses the `path+"[]"` sibling element for its breakdown; `CategoricalMaxDistinct=25`
is the largest legible top-k, and a promoted field (`!DistinctExact`) is high-card.

**EnumLike flag** (independent of form):
```
enumLike = fp.DistinctExact
        && EnumMinDistinct <= fp.DistinctCount <= EnumMaxDistinct
        && fp.DistinctCount == len(fp.TopValues)
        && dom == "string"
```

## 4. Histogram display re-binning (N=20, point-mass)

`fp.Histogram` is <=64 variable-width streaming centroids; true extent is
`*fp.Min`/`*fp.Max`. Re-bin into `DisplayBins=20` equal-width bars using
point-mass at the centroid (each centroid drops its whole `Count` into the bar
containing its `Value`). This keeps counts integer and makes `Σ bins == total`
exactly.

```
displayHistogram(fp):
  lo, hi := *fp.Min, *fp.Max
  total  := Σ fp.Histogram[i].Count
  if hi <= lo:                                   // constant field
      return Histogram{Min:lo, Max:hi, BinWidth:0, Total:total, MaxCount:total,
                       Bins:[HistBar{Lo:lo, Hi:hi, Count:total, Frac:1, Label:fmtNum(lo)}]}
  w := (hi - lo) / DisplayBins
  counts := [DisplayBins]int{}
  for b in fp.Histogram:
      idx := int((b.Value - lo) / w)             // floor
      if idx < 0            { idx = 0 }
      if idx >= DisplayBins { idx = DisplayBins - 1 }   // Value==hi clamps to last bin
      counts[idx] += b.Count
  maxCount := max(counts)                          // 0 -> all Frac 0
  for i in 0..DisplayBins-1:
      Bins[i] = HistBar{Lo: lo+i*w, Hi: lo+(i+1)*w, Count: counts[i],
                        Frac: safeDiv(counts[i], maxCount),
                        Label: fmtNum(lo+i*w)+"–"+fmtNum(lo+(i+1)*w)}
  return Histogram{Min:lo, Max:hi, BinWidth:w, Bins:Bins, MaxCount:maxCount, Total:total}
```

Documented approximation (test it): a centroid's whole count lands in one bar, and
extreme centroids drift inward, so edge bars may under-count vs the true Min/Max
anchors. Acceptable for a display histogram; numeric stats (§5) supply the exact
min/median/mean/max/p95.

## 5. Sparkline, type-mix, meter, stats

**Sparkline** (`[]SparkPoint`):
- `FormHistogram`: from display bins. `{X:(i+0.5)/len(Bins), Y:Bins[i].Frac}`.
- `FormCategorical`: from bars (desc count). `{X:(i+0.5)/len(Bars), Y:Bars[i].Frac}`.
- Otherwise: `nil`.

**Type-mix** (`[]TypeSegment`, always >=1; iterate `kindOrder`, never the map) over
non-null observations. Fold `TypeDist` into `kindOrder` families (`number=int+float`;
null excluded - it lives on the meter). For each family with folded share `s>0`:
```
Frac = s / (1 - fp.NullRate)          // share of non-null; segments sum to ~1
Count = round(s * fp.Observations)
Percent = round(100*Frac); Offset = running cumulative Frac; Series = kindOrder index
```
`Offset` precomputed so the view never sums.

**Meter** (every card): `PresenceRate=fp.PresenceRate`, `NullRate=fp.NullRate`,
texts via `fmtPct`. `NullStatus`: `>=1.0`->critical, `>=0.50`->serious,
`>=0.20`->warning, else `SevNone`.

**Stats** (`[]Stat`), fixed key order:
- Numeric (`fp.Min != nil`): `min`,`max` exact; if `len(fp.Histogram)>0` add
  `mean` = `Σ(b.Value*b.Count)/Σ b.Count` over centroids (`Approx:true`; profiler
  stores no mean), `median`=`*fp.Median` (`Approx`), `p95`=`*fp.P95` (`Approx`);
  `distinct`=`fp.DistinctCount` (`Approx = !fp.DistinctExact`). Order:
  `min, mean, median, p95, max, distinct`.
- String: `distinct` (approx per exactness), `observations`.
- Bool/array/object: `observations` only.
NaN/Inf are never emitted.

### 5.1 Badges + health

Field-level triggers (in order; all firing badges attach; if none fire, attach one
`SevGood` "Clean"):

| Order | Code | Severity | Trigger | Label / Detail |
|---|---|---|---|---|
| 1 | `all_null` | critical | `Observations>0 && NullRate>=1.0` | "All null" / "Every value is null." |
| 2 | `type_drift` | serious | `profile.IsTypeDrift(fp)` | "Mixed types" / non-null kinds+shares |
| 3 | `null_high` | serious | `0.50 <= NullRate < 1.0` | "High null rate" / `fmtPct(NullRate)` |
| 3 | `null_elevated` | warning | `0.20 <= NullRate < 0.50` | "Elevated nulls" / `fmtPct(NullRate)` |
| 4 | `high_cardinality` | warning | `Form == FormHighCard` | "High cardinality" / `DistinctText` |
| 5 | `constant` | warning | `DistinctExact && DistinctCount==1 && Observations>0` | "Single value" / "Only one distinct value." |
| - | `clean` | good | none fired | "Clean" / "" |

File-level badges (`Path==""`): `no_records` (critical) when `Records==0`;
`skipped_records` (warning) when `Skipped>0`.

`FieldCard.Status` = worst badge severity on the card. `VisualModel.Badges` = all
field+file badges sorted by (severity desc, `Path` asc, `Code` asc); per-card
`Badges` sorted by (severity desc, `Code` asc).

**Health score (0-100)** - a field is as unhealthy as its worst badge (no stacking),
averaged across fields, minus a bounded skip penalty:
```go
var fieldPenalty = map[Severity]float64{
	SevGood: 0.00, SevWarning: 0.15, SevSerious: 0.50, SevCritical: 1.00,
}
const SkipPenaltyMax = 20
```
```
F := len(Fields)                                     // per-field cards only (exclude file-level)
raw := 100.0
if F > 0 { raw = 100 * (1 - (Σ fieldPenalty[card.Status]) / F) }
skipRatio   := safeDiv(Skipped, Records + Skipped)
skipPenalty := round(SkipPenaltyMax * skipRatio)
HealthScore := clamp(round(raw) - skipPenalty, 0, 100)
```
Grade band -> `HealthGrade` / `HealthSeverity`:
```
>=90 "Excellent" SevGood  |  >=75 "Good" SevGood  |  >=50 "Fair" SevWarning
>=25 "Poor" SevSerious    |  else "Critical" SevCritical
```

## 6. DiffVisualModel (maps diff.DiffResult)

```go
type DiffVisualModel struct {
	Old             string      `json:"old"`
	New             string      `json:"new"`
	Breaking        bool        `json:"breaking"`
	Verdict         string      `json:"verdict"`
	VerdictSeverity Severity    `json:"verdictSeverity"`
	KPIs            []KPITile   `json:"kpis"`
	Groups          []DiffGroup `json:"groups"`
	Badges          []Badge     `json:"badges"`
	Caveats         []string    `json:"caveats,omitempty"`
}

type DiffGroup struct {
	Kind  string    `json:"kind"`
	Label string    `json:"label"`
	Count int       `json:"count"`
	Rows  []DiffRow `json:"rows"`
}

type DiffRow struct {
	Path     string       `json:"path"`
	Kind     string       `json:"kind"`
	Breaking bool         `json:"breaking"`
	Severity Severity     `json:"severity"`
	Icon     string       `json:"icon"`
	Label    string       `json:"label"`
	Details  []DiffDetail `json:"details"`
}

type DiffDetail struct {
	Reason   string   `json:"reason"`
	Message  string   `json:"message"`
	Old      string   `json:"old"`
	New      string   `json:"new"`
	Breaking bool     `json:"breaking"`
	Severity Severity `json:"severity"`
}
```

Mapping (`FromDiff`):
- Header/KPI: copy `Old,New`, `HasBreaking()`, `Caveats`. KPI tiles (`Value=fmtInt`):
  `compared`; `added` (`SevGood` if >0); `removed`/`changed` (`SevWarning` if >0);
  `breaking` (`Hero:true`, `SevCritical` if >0 else `SevGood`).
- Verdict: `Breaking>0`->"Breaking changes"/`SevCritical`; else `len(Changes)>0`->
  "Compatible changes"/`SevWarning`; else "No changes"/`SevGood`.
- Groups: partition `d.Changes` (already path-sorted) by `Change.Kind`, fixed order
  added->removed->changed; omit empty groups (counts still in KPIs).
- Row severity: `Breaking`->`SevCritical` (Label "Breaking"); else `added`->`SevGood`;
  else non-breaking removed/changed -> `SevWarning`. `Icon=severityIcon[Severity]`.
- Details: map each `diff.Detail` 1:1; `Reason=string(Detail.Reason)`; empty
  `Old`/`New`->"-"; `Severity = SevCritical if Breaking else SevWarning`.

### 6.1 Diff-derived critical badges (from d.Changes only)
- `field_removed` - critical, per `Change{Kind==Removed && Breaking}`:
  detail "Always-present field '<path>' was removed."
- `type_narrowing` - critical, per `Change` with a `Detail{Reason==ReasonType, Breaking}`
  whose type set NARROWED: split `Detail.Old`/`Detail.New` on `,`; narrowing =
  `New ⊊ Old` (every New token in Old and `len(New) < len(Old)`). Detail
  "Field '<path>' narrowed its type set (<Old> -> <New>)."
Both carry `Path`, `Icon=severityIcon[SevCritical]`; sorted by (severity desc, path asc, code asc).

## 7. Formatting helpers (stdlib, deterministic)

```
fmtPct(f)   -> strconv.Itoa(int(f*100+0.5)) + "%"        // matches diff.pct
fmtInt(n)   -> thousands-grouped, "12,480" (manual, sign-prefixed)
fmtNum(f)   -> NaN/Inf -> "-"; |v|>=1e12 T; >=1e9 B; >=1e6 M; >=1e4 K (trim1);
               integer-valued -> FormatInt; else FormatFloat(f,'f',2,64) trailing-zeros trimmed
fmtDistinct(n, exact) -> fmtInt(n) with "~" prefix when !exact
safeDiv(a,b) -> 0 if b==0 else a/b
```
`trim1(x)` = `FormatFloat(x,'f',1,64)` minus a trailing `.0`. Compact suffixing
starts at `1e4`.

Format derivation (`Options.Format==""`): from the `Name`/`Source` extension -
`.csv`->CSV, `.tsv`->TSV, `.parquet`/`.pqt`->Parquet,
`.sqlite`/`.sqlite3`/`.db`->SQLite, `.ndjson`/`.jsonl`->NDJSON, `.json`->JSON,
`""`->"-", else uppercased extension.

## 8. Testing seams
- `FromProfile` goldens: numeric-continuous, discrete-numeric, low-card/enum string,
  high-card string, mixed-type, all-null, bool, array container + its `[]` element,
  promoted (`!DistinctExact`), empty file.
- `displayHistogram` unit: `Σ Count==total`; degenerate `Min==Max`->1 bar; single
  centroid; boundary value floors to higher bin; `Value==Max` clamps to last bin.
- `selectForm` boundary: distinct 12/13, 25/26, null 0.19/0.20/0.49/0.50/1.0, drift
  on/off, array, bool, all-null.
- Health: all-clean->100, all-critical->0, mixed+skip -> exact int + grade band.
- `FromDiff` goldens: grouping/order, breaking->critical, verdict selection,
  `field_removed`/`type_narrowing` badges, empty-diff, caveats passthrough.
- Dual-consumer: every `FieldCard` has non-nil `Meter`,`Stats`,`Badges`, and a
  non-nil `TypeMix` EXCEPT when `Form==FormEmpty` (all-null / no observations),
  where `TypeMix` is `nil` (JSON `null`) because there is no non-null mass to
  compose. **Renderer note (P3/P6):** gate type-mix rendering on
  `Form !== "empty"` - a naive `card.typeMix.map(...)` would throw on the empty
  card. The empty card is fully described by `Form:"empty"`, `Kind:"empty"`,
  `Status:"critical"`, and `Meter.nullRate==1`.

## 9. Flags carried for the plan author (all resolved; none blocking)
1. `Options.Format` extends the spec signature - the pipeline/GUI must pass a label.
2. Bool true/false split is unrecoverable from the current profiler -> bool = meter.
3. String length is min/max only -> `StrLenBar` is a range track, not a histogram.
4. `mean` is derived from centroids (no stored mean), approximate like median/p95.
5. `[]` element fields render both as their own card and (via
   `ArrayBreakdown.ElementPath`) nestable under the array container - link, never duplicate.
6. Per-bar / per-segment `Percent` is independently rounded, so a set can sum to
   99 or 101. It is display text only; geometry consumers use `Frac`/`Count`
   (exact). Do not assert `Σ Percent == 100`; if P3 needs pixel-perfect stacking,
   drive widths from `Frac` (or apply largest-remainder rounding in the view).
7. Placeholder glyph for a missing value is EM DASH `-` (U+2014) everywhere
   (`fmtNum`, `deriveFormat`, empty diff `Old`/`New`); the EN DASH `–` (U+2013)
   appears only as a numeric-range separator (histogram bin labels, `StrLenBar`).
   Separately, the differ's own preformatted present/absent marker `-` (ASCII
   hyphen) passes through diff detail text verbatim per §6 - a diff view may see
   both `-` and `-`; normalize in the renderer if desired.
