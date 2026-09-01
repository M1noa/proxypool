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
// it streams rather than returning a []byte because -skip-check publishes every
// merged record — millions of them, all still resident — and buffering the whole
// encoding to hand to os.WriteFile would cost that again for as long as the
// write takes. a checked run publishes a few thousand. bufio latches the first
// write error and no-ops after it, so the single Flush check covers every write
// above it.
func EncodeTo(w io.Writer, records []*Record) error {
	b := bufio.NewWriterSize(w, 1<<16)
	// one scratch slice for every record's pairs instead of 21 fresh kv per
	// record: the pairs are consumed before the next record is reached
	var pairs []kv
	writeArray(b, records, 0, func(b *bufio.Writer, r *Record, ind int) {
		pairs = recordPairs(pairs, r)
		writeObjectPairs(b, pairs, ind)
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

// recordPairs appends r's pairs into dst, reusing its backing array.
func recordPairs(dst []kv, r *Record) []kv {
	pairs := append(dst[:0],
		kv{"ip", r.IP},
		kv{"ip_version", r.IPVersion},
		kv{"port", r.Port},
		kv{"protocols", r.Protocols},
		kv{"country", r.Country},
		kv{"anonymity", r.Anonymity},
		kv{"https", r.HTTPS},
		kv{"sources", r.Sources},
		kv{"source_meta", r.SourceMeta},
		kv{"last_checked", derefAny(r.LastChecked)},
		kv{"response_time_ms", derefAny(r.ResponseTimeMS)},
		kv{"response_time_raw_ms", derefAny(r.ResponseTimeRawMS)},
		kv{"asn", derefAny(r.ASN)},
		kv{"as_org", r.AsOrg},
		kv{"ip_type", r.IPType},
	)
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
	pad := indentPad(indent + 1)
	for i, it := range items {
		b.WriteString(pad)
		writeItem(b, it, indent+1)
		if i < len(items)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indentPad(indent))
	b.WriteByte(']')
}

// indentPad slices a fixed string instead of building one. nesting never gets
// past a handful of levels, but every array and every object asks twice, so the
// count tracks the number of records written rather than the depth.
const pads = "                                " // 32 spaces, 16 levels

func indentPad(indent int) string {
	if n := indent * 2; n <= len(pads) {
		return pads[:n]
	}
	return strings.Repeat("  ", indent)
}

func writeObjectPairs(b *bufio.Writer, pairs []kv, indent int) {
	if len(pairs) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	pad := indentPad(indent + 1)
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
	b.WriteString(indentPad(indent))
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
				writeU16(b, r)
			case r < 0x80:
				b.WriteByte(byte(r))
			case r <= 0xFFFF:
				writeU16(b, r)
			default:
				r -= 0x10000
				writeU16(b, 0xD800+(r>>10))
				writeU16(b, 0xDC00+(r&0x3FF))
			}
		}
	}
	b.WriteByte('"')
}

// writeU16 is fmt.Fprintf(b, `\u%04x`, v) without boxing the rune into an any
// and re-parsing the verb. as_org carries non-ascii, so this is a hot path.
const hexDigits = "0123456789abcdef"

func writeU16(b *bufio.Writer, v rune) {
	b.WriteString(`\u`)
	b.WriteByte(hexDigits[(v>>12)&0xF])
	b.WriteByte(hexDigits[(v>>8)&0xF])
	b.WriteByte(hexDigits[(v>>4)&0xF])
	b.WriteByte(hexDigits[v&0xF])
}
