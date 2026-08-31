package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// StripJSONC removes // line comments, matching lib/util.py load_jsonc.
//
// deliberately narrow: only // comments, and only outside strings. no /* */,
// no trailing commas. a permissive json5 parser would accept sources.jsonc
// variants the python rejected, silently changing which sources load.
//
// the python iterates runes; this iterates bytes. equivalent, because every
// character it tests for is ascii and utf-8 multibyte sequences never contain
// an ascii byte.
func StripJSONC(text []byte) []byte {
	out := make([]byte, 0, len(text))
	inStr := false
	esc := false
	for i := 0; i < len(text); {
		c := text[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			i++
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		default:
			out = append(out, c)
		}
		i++
	}
	return out
}

// LoadJSONC reads a jsonc file and unmarshals it into v.
func LoadJSONC(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(StripJSONC(raw), v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
