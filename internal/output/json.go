package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/M1noa/proxypool/internal/pyfmt"
)

// EncodeTo ports write_outputs' json.dumps(records, indent=2): array of
// objects, fixed key order per record (15 keys, or 21 with Check set), no
// trailing newline, non-ascii escaped to \uXXXX, <>& left unescaped - the
// opposite of encoding/json's defaults on both axes, hence hand-rolled.
//
// source_meta values come from internal/extract, decoded with UseNumber so a
// json.Number renders using its original source text rather than float64's
// reformatting. its keys are sorted alphabetically because the map they arrive
// in has no order of its own; python emitted them in the order each source
// declared them, which is the one place this file's bytes differ from what
// python wrote.
//
// it streams because a full run's array is a few hundred megabytes and the
// records it is built from are all still resident: buffering the whole thing to
// hand to os.WriteFile costs that much again for as long as the write takes.
// bufio latches the first write error and no-ops after it, so the single Flush
// check covers every write above it.
func EncodeTo(w io.Writer, records []*Record) error {
	b := bufio.NewWriterSize(w, 1<<16)
	writeArray(b, records, 0, func(b *bufio.Writer, r *Record, ind int) {
		writeObjectPairs(b, recordPairs(r), ind)
	})
	return b.Flush()
}

// Encode returns what EncodeTo writes. only tests want the whole array in
// memory at once; the pipeline streams it to the file.
func Encode(records []*Record) []byte {
	var buf bytes.Buffer
	_ = EncodeTo(&buf, records) // a bytes.Buffer write cannot fail
	return buf.Bytes()
}

type kv struct {
	key string
	val any
}

func recordPairs(r *Record) []kv {
	pairs := []kv{
		{"ip", r.IP},
		{"ip_version", r.IPVersion},
		{"port", r.Port},
		{"protocols", r.Protocols},
		{"country", r.Country},
		{"anonymity", r.Anonymity},
		{"https", r.HTTPS},
		{"sources", r.Sources},
		{"source_meta", r.SourceMeta},
		{"last_checked", derefAny(r.LastChecked)},
		{"response_time_ms", derefAny(r.ResponseTimeMS)},
		{"response_time_raw_ms", derefAny(r.ResponseTimeRawMS)},
		{"asn", derefAny(r.ASN)},
		{"as_org", r.AsOrg},
		{"ip_type", r.IPType},
	}
	if c := r.Check; c != nil {
		pairs = append(pairs,
			kv{"reliability", c.Reliability},
			kv{"quality", c.Quality},
			kv{"checks_total", c.ChecksTotal},
			kv{"checks_ok", c.ChecksOK},
			kv{"first_seen", derefAny(c.FirstSeen)},
			kv{"last_seen", derefAny(c.LastSeen)},
		)
	}
	return pairs
}

func derefAny[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func writeValue(b *bufio.Writer, v any, indent int) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJSONString(b, t)
	case json.Number:
		b.WriteString(string(t))
	case int:
		b.WriteString(strconv.Itoa(t))
	case float64:
		b.WriteString(pyfmt.Float(t))
	case []string:
		writeArray(b, t, indent, func(b *bufio.Writer, s string, _ int) { writeJSONString(b, s) })
	case []any:
		writeArray(b, t, indent, writeValue)
	case map[string]any:
		writeObjectPairs(b, mapToPairs(t), indent)
	default:
		panic(fmt.Sprintf("output: unsupported value type %T", v))
	}
}

func mapToPairs(m map[string]any) []kv {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]kv, len(keys))
	for i, k := range keys {
		pairs[i] = kv{k, m[k]}
	}
	return pairs
}

// writeArray renders a json array with python's indent=2 layout: "[]" when
// empty, otherwise one item per line at indent+1.
func writeArray[T any](b *bufio.Writer, items []T, indent int, writeItem func(*bufio.Writer, T, int)) {
	if len(items) == 0 {
		b.WriteString("[]")
		return
	}
	b.WriteString("[\n")
	pad := strings.Repeat("  ", indent+1)
	for i, it := range items {
		b.WriteString(pad)
		writeItem(b, it, indent+1)
		if i < len(items)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat("  ", indent))
	b.WriteByte(']')
}

func writeObjectPairs(b *bufio.Writer, pairs []kv, indent int) {
	if len(pairs) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	pad := strings.Repeat("  ", indent+1)
	for i, p := range pairs {
		b.WriteString(pad)
		writeJSONString(b, p.key)
		b.WriteString(": ")
		writeValue(b, p.val, indent+1)
		if i < len(pairs)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat("  ", indent))
	b.WriteByte('}')
}

// writeJSONString matches json.dumps' default string escaping: quote,
// backslash and control chars get short escapes, other control chars and
// every non-ascii code point become \uXXXX (surrogate pairs above U+FFFF),
// and <>& are left alone.
func writeJSONString(b *bufio.Writer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(b, `\u%04x`, r)
			case r < 0x80:
				b.WriteRune(r)
			case r <= 0xFFFF:
				fmt.Fprintf(b, `\u%04x`, r)
			default:
				r -= 0x10000
				hi := 0xD800 + (r >> 10)
				lo := 0xDC00 + (r & 0x3FF)
				fmt.Fprintf(b, `\u%04x\u%04x`, hi, lo)
			}
		}
	}
	b.WriteByte('"')
}
