package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/visual"
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

// ProfileFile returns the visual dashboard model for a data file.
func (a *App) ProfileFile(path string) (visual.VisualModel, error) {
	r, err := pipeline.Profile(pipeline.Options{Path: path, Format: "auto"})
	if err != nil {
		return visual.VisualModel{}, err
	}
	return visual.FromProfile(r, visual.Options{Name: r.Source}), nil
}

// DiffFiles returns the visual comparison model between two data files.
func (a *App) DiffFiles(oldPath, newPath string) (visual.DiffVisualModel, error) {
	oldR, err := pipeline.Profile(pipeline.Options{Path: oldPath, Format: "auto"})
	if err != nil {
		return visual.DiffVisualModel{}, err
	}
	newR, err := pipeline.Profile(pipeline.Options{Path: newPath, Format: "auto"})
	if err != nil {
		return visual.DiffVisualModel{}, err
	}
	d := diff.Diff(oldR, newR)
	return visual.FromDiff(d), nil
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
