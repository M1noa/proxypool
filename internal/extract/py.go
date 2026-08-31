package extract

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// python semantics the port depends on. lib/parse.py leans on str(), `or`
// truthiness and re's unicode-aware \s, none of which map onto go's defaults,
// so they are spelled out here rather than approximated at each call site.

// pySpace is python re's \s for str patterns. go's \s is only [\t\n\f\r ]:
// it misses \v, the \x1c-\x1f separators, and all of \p{Z} — nbsp above all,
// which scraped html is full of.
const pySpace = `\t\n\v\f\r\x{1c}-\x{1f}\x{85}\p{Z}`

var (
	// _norm_record's protocols split, and parse_protocol's, which differ:
	// the second also breaks on a pipe.
	reProtosSplit = regexp.MustCompile(`[,/` + pySpace + `]+`)
	reProtoSplit  = regexp.MustCompile(`[/,|` + pySpace + `]+`)
)

// pyStrip is str.strip(). go's strings.TrimSpace uses unicode.IsSpace, which
// is missing \x1c-\x1f — python counts those four separators as whitespace.
func pyStrip(s string) string {
	return strings.TrimFunc(s, isPySpace)
}

func isPySpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

// dig is a dotted path lookup. an empty path returns obj unchanged.
func dig(obj any, path string) any {
	cur := obj
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m[part]
		if !ok {
			return nil
		}
		cur = v
	}
	return cur
}

// truthy is python's `if v`. json.Number is checked numerically: it is a
// defined string type, so a `case string` arm would never see it.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case float64:
		return t != 0
	case int:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// pystr is python's str() for the types json decoding produces.
func pystr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return t
	case json.Number:
		// the literal as written, which is what repr gives for an int and for
		// a float with a decimal point. 1e3 renders as "1000.0" in python and
		// stays "1e3" here.
		return string(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e16 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int:
		return strconv.Itoa(t)
	}
	return fmt.Sprint(v)
}

// strOr is `str(v or "")`, the shape every string read in _norm_record takes.
func strOr(v any) string {
	if !truthy(v) {
		return ""
	}
	return pystr(v)
}

// pyNumber is isinstance(v, (int, float)) plus the value. python's bool is an
// int subclass, so True passes the check and floats to 1.
func pyNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case int:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// toBool ports lib/util.to_bool.
func toBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	switch strings.ToLower(pyStrip(strOr(v))) {
	case "true", "yes", "1":
		return true
	case "false", "no", "0", "", "none":
		return false
	}
	return truthy(v)
}

// normalizeAnon ports lib/util.normalize_anon. order matters: "high" wins
// over "transparent", and the "not" guard keeps "not anonymous" out of the
// anonymous bucket.
func normalizeAnon(v any) string {
	s := strings.ToLower(pyStrip(strOr(v)))
	switch {
	case strings.Contains(s, "elite"), strings.Contains(s, "high"),
		s == "ha", s == "高匿":
		return "elite"
	case s == "anon":
		return "anonymous"
	case s == "trans":
		return "transparent"
	case strings.Contains(s, "anon") && !strings.Contains(s, "not"):
		return "anonymous"
	case strings.Contains(s, "transparent"),
		s == "low", s == "透明", s == "notanonymous":
		return "transparent"
	case s == "average", s == "medium", s == "normal", s == "普匿":
		return "anonymous"
	}
	return ""
}

// parseProtocol splits a protocol-ish value into known protocols.
func parseProtocol(v any) []string {
	var out []string
	for _, p := range reProtoSplit.Split(strings.ToLower(pyStrip(strOr(v))), -1) {
		switch p {
		case "http", "https", "socks4", "socks5":
			out = append(out, p)
		}
	}
	return out
}

// compileMatch anchors a pattern at the start of the subject the way
// re.match does. \A rather than ^ so an inline (?m) in the pattern cannot
// turn it into a per-line anchor.
func compileMatch(rx string) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(?:` + rx + `)`)
}

// groupDict is m.groupdict(). python yields None for a group that did not
// participate and go yields "", but both are falsy and every reader
// downstream goes through `or ""`, so the distinction never surfaces.
func groupDict(re *regexp.Regexp, m []string) map[string]any {
	out := map[string]any{}
	for i, name := range re.SubexpNames() {
		if name != "" {
			out[name] = m[i]
		}
	}
	return out
}

// pySplitLines is str.splitlines: eleven boundary characters, not just \n,
// and no trailing empty element. sources served with bare \r line endings
// would otherwise arrive as one enormous line.
func pySplitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isLineBoundary(r) {
			i += w
			continue
		}
		out = append(out, s[start:i])
		i += w
		if r == '\r' && i < len(s) && s[i] == '\n' {
			i++
		}
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
