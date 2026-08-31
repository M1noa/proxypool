package extract

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/M1noa/proxypool/internal/config"
)

// DecodeJSON unmarshals with UseNumber so numeric literals keep the text they
// were written with. it matters twice: `int(str(port))` fails on "8080.0" and
// succeeds on "8080", and source_meta values are re-serialized, where a
// float64 round-trip would turn 3 into 3.0.
func DecodeJSON(s string) (any, error) {
	d := json.NewDecoder(bytes.NewReader([]byte(s)))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

type field struct {
	name string
	spec config.Spec
}

// jsonFields is the field set _extract_json reads, in its order. html reads
// only seven of these and text reads none of them.
func jsonFields(ex *config.Extract) []field {
	return []field{
		{"ip", ex.IP}, {"port", ex.Port},
		{"protocol", ex.Protocol}, {"protocols", ex.Protocols},
		{"country", ex.Country}, {"country_name", ex.CountryName},
		{"anonymity", ex.Anonymity}, {"https", ex.HTTPS},
		{"response_time", ex.ResponseTime},
	}
}

// extractJSON ports lib/parse._extract_json. items are either objects, dug
// field by field, or bare strings matched against extract.regex.
func extractJSON(items []any, ex *config.Extract) ([]map[string]any, error) {
	var re *regexp.Regexp
	if ex.Regex != "" {
		var err error
		if re, err = compileMatch(ex.Regex); err != nil {
			return nil, err
		}
	}
	fields := jsonFields(ex)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			if re == nil {
				continue
			}
			if m := re.FindStringSubmatch(s); m != nil {
				out = append(out, groupDict(re, m))
			}
			continue
		}
		o := map[string]any{}
		for _, f := range fields {
			if f.spec.Empty() {
				continue
			}
			// the string form is a bare path and the object form is
			// {"path": …, "map": …}; JSONPath collapses the two.
			v := Dig(it, f.spec.JSONPath())
			if f.spec.HasMap() {
				// dict.get, so an unmapped value becomes None rather than
				// passing through
				v = f.spec.Map[PyStr(v)]
			}
			o[f.name] = v
		}
		for mk, mv := range ex.SourceMeta {
			o["meta_"+mk] = Dig(it, mv.JSONPath())
		}
		out = append(out, o)
	}
	return out, nil
}

// rootItems resolves extract.root for a json source and coerces the result to
// a list. a path that misses falls back to the whole document.
func rootItems(doc any, root string) []any {
	items := doc
	if root != "" {
		items = Dig(doc, root)
	}
	if items == nil {
		items = doc
	}
	if list, ok := items.([]any); ok {
		return list
	}
	return []any{items}
}

// rootItemsEmbedded is the same resolution for a json blob lifted out of a
// <script> tag, which drops a missing root instead of falling back to the
// document and yields nothing for a falsy value.
func rootItemsEmbedded(doc any, root string) []any {
	items := doc
	if root != "" {
		items = Dig(doc, root)
	}
	if list, ok := items.([]any); ok {
		return list
	}
	if !Truthy(items) {
		return nil
	}
	return []any{items}
}
