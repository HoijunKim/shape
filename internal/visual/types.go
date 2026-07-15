package visual

import (
	"github.com/hoijun-kim/shape/internal/diff"
)

// Options carries render metadata that ProfileResult does not itself hold.
// ProfileResult.Source is only set on the diff path, and no detected format is
// stored anywhere, so the caller passes them here. This is the one deliberate
// extension to the spec's FromProfile(ProfileResult) signature.
type Options struct {
	Name   string // display name (filename/label); "" -> ProfileResult.Source
	Format string // "JSON"|"NDJSON"|"CSV"|"TSV"|"Parquet"|"SQLite"; "" -> derive from Name/Source ext
}

// FromDiff builds the two-file comparison model.
func FromDiff(d diff.DiffResult) DiffVisualModel {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// 1. Shared value types
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// 2. Whole-file model, KPI tiles, FieldCard
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// 3. Chart-form selection (concrete named-const thresholds)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// 5.1 Badges + health
// ---------------------------------------------------------------------------

var fieldPenalty = map[Severity]float64{
	SevGood: 0.00, SevWarning: 0.15, SevSerious: 0.50, SevCritical: 1.00,
}

const SkipPenaltyMax = 20

// ---------------------------------------------------------------------------
// 6. DiffVisualModel (maps diff.DiffResult)
// ---------------------------------------------------------------------------

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
