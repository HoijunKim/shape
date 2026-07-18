package main

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/query"
	"github.com/hoijun-kim/shape/internal/visual"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound application. Every exported method becomes a callable
// TypeScript binding. App owns exactly one query.Engine and, at most one open
// source at a time: opening a new one closes the previous handle so a memory-
// tier store (up to 512 MiB) or a sqlite connection is never leaked.
type App struct {
	ctx context.Context
	eng *query.Engine

	mu     sync.Mutex
	handle string // current open source handle; "" when none
}

func NewApp() *App { return &App{eng: query.NewEngine()} }

// startup captures the runtime context (wired via OnStartup, not a binding).
func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// reqCtx returns the context requests run under. a.ctx is nil until Wails
// calls startup, and the Go tests never call it, so fall back to Background
// rather than passing a nil ctx into the engine.
func (a *App) reqCtx() context.Context {
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

// OpenSource opens a data file for exploration and returns its structure map,
// column set, and tier (spec §8). Any previously open source is closed first.
func (a *App) OpenSource(req query.OpenRequest) (query.OpenResult, error) {
	res, err := a.eng.OpenSource(a.reqCtx(), req)
	if err != nil {
		return query.OpenResult{}, err
	}
	a.mu.Lock()
	prev := a.handle
	a.handle = res.Handle
	a.mu.Unlock()
	if prev != "" && prev != res.Handle {
		_ = a.eng.CloseSource(prev) // best effort: a stale handle is not the caller's problem
	}
	return res, nil
}

// QueryRows returns one window of rows for an open handle (spec §8).
func (a *App) QueryRows(req query.QueryRequest) (query.RowSet, error) {
	return a.eng.QueryRows(a.reqCtx(), req)
}

// CountMatches returns the exact match count for a filter (spec §8).
func (a *App) CountMatches(req query.CountRequest) (query.CountResult, error) {
	return a.eng.CountMatches(a.reqCtx(), req)
}

// Cancel interrupts an in-flight request by id (spec §8). An unknown id is a
// normal race (the request may have just finished) and returns an error the
// caller may ignore.
func (a *App) Cancel(requestID string) error { return a.eng.Cancel(requestID) }

// CloseSource releases a handle's backend.
func (a *App) CloseSource(handle string) error {
	a.mu.Lock()
	if a.handle == handle {
		a.handle = ""
	}
	a.mu.Unlock()
	return a.eng.CloseSource(handle)
}

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
