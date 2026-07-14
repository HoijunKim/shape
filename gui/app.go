package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/profile"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound application. Every exported method becomes a callable
// TypeScript binding.
type App struct {
	ctx context.Context
}

func NewApp() *App { return &App{} }

// startup captures the runtime context (wired via OnStartup, not a binding).
func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// FieldView is a frontend-friendly per-field profile.
type FieldView struct {
	Path         string             `json:"path"`
	PresenceRate float64            `json:"presenceRate"`
	NullRate     float64            `json:"nullRate"`
	TypeDist     map[string]float64 `json:"typeDist"`
	Distinct     int                `json:"distinct"`
	DistinctEx   bool               `json:"distinctExact"`
	Drift        bool               `json:"drift"`
	Min          *float64           `json:"min,omitempty"`
	Max          *float64           `json:"max,omitempty"`
	StrLenMin    *int               `json:"strLenMin,omitempty"`
	StrLenMax    *int               `json:"strLenMax,omitempty"`
	TopValues    []ValueView        `json:"topValues"`
}

// ValueView is a value + its count for the top-values list.
type ValueView struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// ProfileView is the full profile handed to the frontend.
type ProfileView struct {
	Source  string      `json:"source"`
	Records int         `json:"records"`
	Skipped int         `json:"skipped"`
	Fields  []FieldView `json:"fields"`
}

func toView(r profile.ProfileResult) ProfileView {
	pv := ProfileView{Source: r.Source, Records: r.Records, Skipped: r.Skipped}
	for _, f := range r.Fields {
		td := map[string]float64{}
		for k, v := range f.TypeDist {
			td[string(k)] = v
		}
		tv := make([]ValueView, 0, len(f.TopValues))
		for _, v := range f.TopValues {
			tv = append(tv, ValueView{Value: v.Value, Count: v.Count})
		}
		pv.Fields = append(pv.Fields, FieldView{
			Path: f.Path, PresenceRate: f.PresenceRate, NullRate: f.NullRate,
			TypeDist: td, Distinct: f.DistinctCount, DistinctEx: f.DistinctExact,
			Drift: profile.IsTypeDrift(f), Min: f.Min, Max: f.Max,
			StrLenMin: f.StrLenMin, StrLenMax: f.StrLenMax, TopValues: tv,
		})
	}
	return pv
}

// ProfileFile returns the per-field profile of a data file.
func (a *App) ProfileFile(path string) (ProfileView, error) {
	r, err := pipeline.Profile(pipeline.Options{Path: path, Format: "auto"})
	if err != nil {
		return ProfileView{}, err
	}
	return toView(r), nil
}

// SchemaJSON returns the inferred JSON Schema as a pretty-printed string.
func (a *App) SchemaJSON(path string) (string, error) {
	s, err := pipeline.Schema(pipeline.Options{Path: path, Format: "auto"})
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// OpenFileDialog opens the native picker (runtime-only; "" when cancelled).
func (a *App) OpenFileDialog() (string, error) {
	return wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Open a data file"})
}

// SaveText prompts for a save path and writes content there (runtime-only).
func (a *App) SaveText(defaultName, content string) (string, error) {
	path, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{DefaultFilename: defaultName})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
