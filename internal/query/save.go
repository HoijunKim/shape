// Package query: the E7 save path. Unlike ExportQuery (which projects to flat
// Column.Path keys and writes only the filtered/transformed view), SaveEdits
// writes EVERY source record back verbatim -- the reader's nested
// map[string]any, numbers as json.Number -- with a cell-edit overlay applied at
// each edit's SOURCE path, as JSON or NDJSON, to a NEW file. This preserves the
// source's nesting and exact number literals (the read found that reusing the
// export encoders flattened nested sources and lost number precision), and it
// is a COPY only (no overwrite, no filtered-subset-over-source data loss).
package query

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// CellEdit is one edited cell (spec E7): the record at absolute Index, the
// source Path (a dotted path resolveSegs accepts, e.g. "user.name"), and the
// new scalar value carried as Kind + Literal -- NOT a JSON number token, because
// JavaScript cannot hold a >2^53 integer and would round it before the request
// is even serialised. A number's exact source text rides in Literal and becomes
// a json.Number here (fidelity preserved, review F1).
type CellEdit struct {
	Index   int64  `json:"index"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`    // string | int | float | bool | null
	Literal string `json:"literal"` // the value's text: a number literal, the string, "true"/"false", or "" for null
}

// SaveRequest is the SaveEdits request DTO. Format is json|ndjson only (where
// nested write-back is faithful); there is no filter/search/transform (save
// writes the complete file, not a view) and no overwrite flag (copy only).
type SaveRequest struct {
	RequestID string     `json:"requestId,omitempty"`
	Handle    string     `json:"handle"`
	Format    string     `json:"format"`
	OutPath   string     `json:"outPath"`
	Edits     []CellEdit `json:"edits"`
}

// SaveResult is the SaveEdits response DTO. EditsUnapplied is surfaced (not
// hidden): an edit whose path could not be resolved/set, or whose index is past
// the source, is counted here rather than silently dropped.
type SaveResult struct {
	OutPath        string   `json:"outPath"`
	RowsOut        int64    `json:"rowsOut"`
	EditsApplied   int64    `json:"editsApplied"`
	EditsUnapplied int64    `json:"editsUnapplied"`
	BytesOut       int64    `json:"bytesOut"`
	ElapsedMs      int64    `json:"elapsedMs"`
	Warnings       []string `json:"warnings,omitempty"`
}

// shallowCopyMap returns a copy of m one level deep (values shared).
func shallowCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// setAtPath returns rec with the leaf at segs set to val, copying ONLY the
// touched object spine so rec (a backend-owned record) is never mutated. It
// errors on an empty path, an Elem (array-element) segment, a non-object
// record, or a non-object ancestor along the path (it never clobbers an
// existing scalar with an object) -- those become EditsUnapplied, never a
// silent corruption.
func setAtPath(rec any, segs []Seg, val any) (any, error) {
	if len(segs) == 0 {
		return nil, fmt.Errorf("query: setAtPath: empty path")
	}
	for _, s := range segs {
		if s.Elem {
			return nil, fmt.Errorf("query: setAtPath: array-element path is not settable")
		}
	}
	root, ok := rec.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("query: setAtPath: record is not a JSON object")
	}
	out := shallowCopyMap(root)
	cur := out
	for i := 0; i < len(segs)-1; i++ {
		k := segs[i].Key
		existing, exists := cur[k]
		var child map[string]any
		if m2, isMap := existing.(map[string]any); isMap {
			child = shallowCopyMap(m2)
		} else if exists {
			return nil, fmt.Errorf("query: setAtPath: %q is not an object", k)
		} else {
			child = map[string]any{}
		}
		cur[k] = child
		cur = child
	}
	cur[segs[len(segs)-1].Key] = val
	return out, nil
}

// editValueFromLiteral builds the typed value for a CellEdit from its Kind +
// Literal. A number becomes a json.Number holding the EXACT source text (no
// float64 round-trip), validated to be a well-formed JSON number so a malformed
// literal is rejected here (never written). string is the literal verbatim;
// bool parses "true"/"false"; null (and any unknown kind) is nil.
func editValueFromLiteral(kind, literal string) (any, error) {
	switch kind {
	case "string":
		return literal, nil
	case "int", "float", "number":
		// Validate it round-trips as a JSON number (UseNumber keeps the literal).
		dec := json.NewDecoder(bytes.NewReader([]byte(literal)))
		dec.UseNumber()
		var n any
		if err := dec.Decode(&n); err != nil {
			return nil, fmt.Errorf("query: SaveEdits: %q is not a valid number", literal)
		}
		num, ok := n.(json.Number)
		if !ok {
			return nil, fmt.Errorf("query: SaveEdits: %q is not a number", literal)
		}
		return num, nil
	case "bool":
		return literal == "true", nil
	case "null":
		return nil, nil
	default:
		return nil, fmt.Errorf("query: SaveEdits: unknown edit kind %q", kind)
	}
}

// marshalRecord renders one record as compact JSON, json.Number-preserving and
// non-finite-safe (a NaN/±Inf deep in a value -- Parquet DOUBLE / SQLite REAL --
// falls back to a sanitizeValue copy, like compactJSON, rather than erroring).
func marshalRecord(rec any) ([]byte, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		b, err = json.Marshal(sanitizeValue(rec))
		if err != nil {
			return nil, fmt.Errorf("query: SaveEdits: marshal record: %w", err)
		}
	}
	return b, nil
}

// recordWriter streams whole records as a JSON array or NDJSON.
type recordWriter struct {
	w      io.Writer
	ndjson bool
	wrote  bool
}

func newRecordWriter(w io.Writer, format string) (*recordWriter, error) {
	rw := &recordWriter{w: w, ndjson: format == "ndjson"}
	if !rw.ndjson {
		if _, err := io.WriteString(w, "["); err != nil {
			return nil, err
		}
	}
	return rw, nil
}

func (rw *recordWriter) write(rec any) error {
	b, err := marshalRecord(rec)
	if err != nil {
		return err
	}
	if rw.ndjson {
		if _, err := rw.w.Write(b); err != nil {
			return err
		}
		_, err := io.WriteString(rw.w, "\n")
		return err
	}
	if rw.wrote {
		if _, err := io.WriteString(rw.w, ","); err != nil {
			return err
		}
	}
	rw.wrote = true
	_, err = rw.w.Write(b)
	return err
}

func (rw *recordWriter) close() error {
	if rw.ndjson {
		return nil
	}
	_, err := io.WriteString(rw.w, "]\n")
	return err
}

// atomicWriteFile writes via a temp file next to outPath and renames it into
// place only on success (the export path's temp-then-rename, text-only), so a
// failed/cancelled save leaves the destination untouched. Returns bytes written.
func atomicWriteFile(outPath string, write func(w io.Writer) error) (int64, error) {
	dir := filepath.Dir(outPath)
	tmp, err := os.CreateTemp(dir, exportTempPattern)
	if err != nil {
		return 0, fmt.Errorf("query: SaveEdits: creating a temporary file next to %s: %w", outPath, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	counter := &countingWriter{w: tmp}
	buffered := bufio.NewWriterSize(counter, 64<<10)
	if err := write(buffered); err != nil {
		return 0, err
	}
	if err := buffered.Flush(); err != nil {
		return 0, fmt.Errorf("query: SaveEdits: flushing: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return 0, fmt.Errorf("query: SaveEdits: syncing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("query: SaveEdits: closing the temporary file: %w", err)
	}
	perm := os.FileMode(0o644)
	if fi, statErr := os.Stat(outPath); statErr == nil && fi.Mode().IsRegular() {
		perm = fi.Mode().Perm()
	}
	_ = os.Chmod(tmpName, perm)
	if err := os.Rename(tmpName, outPath); err != nil {
		return 0, fmt.Errorf("query: SaveEdits: could not write %s -- it may be open in another program: %w", outPath, err)
	}
	committed = true
	return counter.n, nil
}

// SaveEdits writes every source record back with the edit overlay applied at
// each edit's source path, as JSON/NDJSON, to a NEW file (spec E7). It never
// filters/projects (the whole file is written) and never targets the open
// source (validateExportTarget). Numbers keep their exact literal (json.Number
// via UseNumber). EditsUnapplied = the edits that could not be resolved/set or
// whose index is past the source -- surfaced, not hidden.
func (e *Engine) SaveEdits(ctx context.Context, req SaveRequest, progress func(rows int64)) (SaveResult, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return SaveResult{}, err
	}
	if req.Format != "json" && req.Format != "ndjson" {
		return SaveResult{}, fmt.Errorf("query: SaveEdits: unknown format %q (want json or ndjson)", req.Format)
	}
	if err := validateExportTarget(req.OutPath, e.sourcePath(req.Handle)); err != nil {
		return SaveResult{}, err
	}

	// Index + decode the edits. An edit whose path does not resolve or whose
	// value does not decode is simply never added, so it falls out as unapplied
	// (RowsOut - applied) at the end.
	type decEdit struct {
		segs []Seg
		val  any
	}
	byIndex := make(map[int64][]decEdit, len(req.Edits))
	cm := backend.Columns()
	for _, ed := range req.Edits {
		segs, serr := resolveSegs(ed.Path, cm)
		if serr != nil {
			continue
		}
		val, derr := editValueFromLiteral(ed.Kind, ed.Literal)
		if derr != nil {
			continue
		}
		byIndex[ed.Index] = append(byIndex[ed.Index], decEdit{segs: segs, val: val})
	}

	ctx, release := e.begin(ctx, req.RequestID)
	defer release()
	start := time.Now()

	var rowsOut, applied int64
	bytesOut, werr := atomicWriteFile(req.OutPath, func(w io.Writer) error {
		rw, err := newRecordWriter(w, req.Format)
		if err != nil {
			return err
		}
		serr := backend.StreamRecords(ctx, func(index int64, rec any) error {
			r := rec
			for _, ed := range byIndex[index] {
				nr, e2 := setAtPath(r, ed.segs, ed.val)
				if e2 != nil {
					continue // counted as unapplied via RowsOut-applied below
				}
				r = nr
				applied++
			}
			rowsOut++
			if progress != nil && rowsOut%exportProgressStride == 0 {
				progress(rowsOut)
			}
			return rw.write(r)
		})
		if serr != nil {
			return serr
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return rw.close()
	})
	if werr != nil {
		return SaveResult{}, werr
	}

	return SaveResult{
		OutPath:        req.OutPath,
		RowsOut:        rowsOut,
		EditsApplied:   applied,
		EditsUnapplied: int64(len(req.Edits)) - applied,
		BytesOut:       bytesOut,
		ElapsedMs:      time.Since(start).Milliseconds(),
	}, nil
}
