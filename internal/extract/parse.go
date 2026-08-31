// package extract ports lib/parse.py's extraction half: turn one source's raw
// body into records, per format — regex over text, dotted paths over json, css
// selectors over html — then normalize each into a Record.
package extract

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/M1noa/proxypool/internal/config"
)

// ParseContent ports lib/parse.parse_content: fetched bytes plus a source
// definition in, normalized records out.
//
// defaults carries the const fields from a urls[] entry. extracted values win,
// but an empty extracted value never clobbers a default.
func ParseContent(src *config.Source, content string, defaults map[string]any) ([]*Record, error) {
	ex := src.EffectiveExtract()
	proto := src.Protocol

	var raws []map[string]any
	var err error
	switch src.Fmt() {
	case "json":
		var doc any
		if doc, err = DecodeJSON(content); err != nil {
			return nil, err
		}
		raws, err = extractJSON(rootItems(doc, ex.Root), ex)
	case "text":
		raws, err = extractText(content, ex)
	case "html":
		if ex.EmbeddedJSON != nil {
			return parseEmbedded(src, content, defaults)
		}
		raws, err = extractHTML(content, ex)
	}
	if err != nil {
		return nil, err
	}

	recs := make([]*Record, 0, len(raws))
	for _, r := range raws {
		rec := NormRecord(merge(r, defaults), src, proto)
		if rec == nil {
			continue
		}
		if src.Fmt() == "text" {
			// a trailing country on a text row is a NAME, not an iso code, so a
			// two-letter one has already been consumed as `country` and anything
			// longer has to be moved across by hand.
			if c := strOr(r["country"]); c != "" && rec.Country == "" {
				rec.CountryName = c
				delete(rec.SourceMeta, "country")
			}
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// parseEmbedded handles a json payload carried inside a <script> tag. the
// blob is extracted with html tooling and then read with the json rules.
func parseEmbedded(src *config.Source, content string, defaults map[string]any) ([]*Record, error) {
	ex := src.EffectiveExtract()
	emb := ex.EmbeddedJSON

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	if emb.Regex != "" {
		// python passes re.S, so . has to cross newlines here too
		if re, err = regexp.Compile(`(?s)` + emb.Regex); err != nil {
			return nil, err
		}
	}
	sel := emb.Selector
	if sel == "" {
		sel = "script"
	}

	var payload any
	doc.Find(sel).EachWithBreak(func(_ int, el *goquery.Selection) bool {
		text := nodeString(el)
		if re != nil {
			m := re.FindStringSubmatch(text)
			if len(m) < 2 {
				return true
			}
			text = m[1]
		}
		v, err := DecodeJSON(PyStrip(text))
		if err != nil {
			return true
		}
		payload = v
		return false
	})
	// nothing decoded, or the blob decoded to a bare null — python's `doc is
	// None` check cannot tell those apart and neither does this.
	if payload == nil {
		return nil, nil
	}

	raws, err := extractJSON(rootItemsEmbedded(payload, ex.Root), ex)
	if err != nil {
		return nil, err
	}
	recs := make([]*Record, 0, len(raws))
	for _, r := range raws {
		if rec := NormRecord(merge(r, defaults), src, src.Protocol); rec != nil {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

// merge layers extracted values over a urls[] entry's const fields. only None
// and "" are treated as absent, so a false or a zero still wins.
func merge(r, defaults map[string]any) map[string]any {
	if len(defaults) == 0 {
		return r
	}
	out := make(map[string]any, len(defaults)+len(r))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range r {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		out[k] = v
	}
	return out
}
