package jsonreader

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Mode selects how the reader interprets the input stream.
type Mode int

const (
	// WholeMode decodes one JSON document; an array streams its elements.
	WholeMode Mode = iota
	// LineMode decodes NDJSON: one JSON value per line, skipping malformed lines.
	LineMode
)

// DetectMode chooses a Mode from a path, an explicit format flag, and a peek.
func DetectMode(path, formatFlag string, peek []byte) Mode {
	switch formatFlag {
	case "json":
		return WholeMode
	case "ndjson":
		return LineMode
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".ndjson"), strings.HasSuffix(lower, ".jsonl"):
		return LineMode
	case strings.HasSuffix(lower, ".json"):
		return WholeMode
	}
	for _, b := range peek {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b == '[' {
			return WholeMode
		}
		return LineMode
	}
	return LineMode
}

// Stream yields decoded records from an input reader.
type Stream struct {
	mode    Mode
	dec     *json.Decoder // WholeMode
	inArray bool
	started bool
	sc      *bufio.Scanner // LineMode
	skipped int
	done    bool
}

// New builds a Stream over r in the given mode.
func New(r io.Reader, mode Mode) *Stream {
	s := &Stream{mode: mode}
	if mode == LineMode {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		s.sc = sc
		return s
	}
	dec := json.NewDecoder(r)
	dec.UseNumber()
	s.dec = dec
	return s
}

// Next returns the next record, or io.EOF when the stream is exhausted.
func (s *Stream) Next() (any, error) {
	if s.mode == LineMode {
		return s.nextLine()
	}
	return s.nextWhole()
}

func (s *Stream) nextLine() (any, error) {
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			s.skipped++
			continue
		}
		return v, nil
	}
	if err := s.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *Stream) nextWhole() (any, error) {
	if s.done {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		tok, err := s.dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); ok && d == '[' {
			s.inArray = true
		} else {
			// Not an array: the token we read is the start of a single value.
			// Re-decode from scratch is not possible, so handle the two shapes:
			// scalar/true/false/null tokens are complete values; object/array
			// delimiters need full decoding. Simplest robust path: only arrays
			// stream; everything else is decoded as one value below.
			s.inArray = false
			return s.decodeSingleAfterToken(tok)
		}
	}
	if s.inArray {
		if !s.dec.More() {
			s.done = true
			return nil, io.EOF
		}
		var v any
		if err := s.dec.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
	s.done = true
	return nil, io.EOF
}

// decodeSingleAfterToken reconstructs a single non-array document whose first
// token has already been consumed.
func (s *Stream) decodeSingleAfterToken(tok json.Token) (any, error) {
	s.done = true
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			// Decode the rest of the object by reading key/value tokens.
			return s.decodeObjectBody()
		}
		return nil, io.EOF
	default:
		return t, nil // scalar, bool, or null value
	}
}

func (s *Stream) decodeObjectBody() (any, error) {
	obj := map[string]any{}
	for s.dec.More() {
		keyTok, err := s.dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		var val any
		if err := s.dec.Decode(&val); err != nil {
			return nil, err
		}
		obj[key] = val
	}
	// consume closing '}'
	if _, err := s.dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	return obj, nil
}

// Skipped returns how many malformed inputs were skipped.
func (s *Stream) Skipped() int { return s.skipped }
