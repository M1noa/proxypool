package extract

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/M1noa/proxypool/internal/config"
)

// htmlFields is the field set _extract_html reads. it is deliberately shorter
// than the json one: no protocols, no country_name. an html source that wants
// several protocols per row has to spell them into `protocol`.
func htmlFields(ex *config.Extract) []field {
	return []field{
		{"ip", ex.IP}, {"port", ex.Port}, {"protocol", ex.Protocol},
		{"country", ex.Country}, {"anonymity", ex.Anonymity},
		{"https", ex.HTTPS}, {"response_time", ex.ResponseTime},
	}
}

// extractHTML ports lib/parse._extract_html.
//
// x/net/html follows the html5 tree construction algorithm and synthesizes a
// <tbody> around bare <tr>s; bs4's html.parser does not. nine of the thirteen
// html row selectors name tbody, so those sources must already serve it
// literally, and go matching more rows than python is the only direction the
// divergence can run.
func extractHTML(htmlText string, ex *config.Extract) ([]map[string]any, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlText))
	if err != nil {
		return nil, err
	}
	fields := htmlFields(ex)
	var out []map[string]any
	doc.Find(ex.RowSel()).Each(func(_ int, row *goquery.Selection) {
		o := map[string]any{}
		for _, f := range fields {
			if f.spec.Empty() {
				continue
			}
			if f.spec.HasConst {
				o[f.name] = f.spec.Const
				continue
			}
			v, e := cellText(row, f.spec)
			if e != nil && err == nil {
				err = e
			}
			o[f.name] = v
		}
		for mk, sel := range ex.SourceMeta {
			v, e := cellText(row, sel)
			if e != nil && err == nil {
				err = e
			}
			o["meta_"+mk] = v
		}
		out = append(out, o)
	})
	return out, err
}

// cellText ports lib/parse._cell_text.
//
// the precedence looks accidental but is load-bearing: regex returns before
// attr and decode are consulted, so a spec carrying both a regex and an attr
// only ever uses the regex.
func cellText(row *goquery.Selection, sel config.Spec) (string, error) {
	el := row
	if s := sel.CSSSelector(); s != "" {
		el = row.Find(s).First()
	}
	if el.Length() == 0 {
		return "", nil
	}

	if sel.Regex != "" {
		re, err := regexp.Compile(sel.Regex)
		if err != nil {
			return "", err
		}
		if re.NumSubexp() < 1 {
			// python would raise IndexError on m.group(1) and fail the whole
			// source; there is no such spec in sources.jsonc.
			return "", nil
		}
		// prefer plain text (href/class noise), fall back to full markup
		matches := group1(re, getText(el))
		if len(matches) == 0 {
			markup, err := goquery.OuterHtml(el)
			if err != nil {
				return "", err
			}
			matches = group1(re, markup)
		}
		return strings.Join(matches, "/"), nil
	}

	var val string
	if sel.Attr != "" {
		val, _ = el.Attr(sel.Attr)
	} else {
		val = getText(el)
	}
	switch sel.Decode {
	case "base64":
		s, ok := pyB64Decode(val)
		if !ok {
			return "", nil
		}
		val = s
	case "base64_reverse":
		// base64 of the reversed string (proxyverity)
		s, ok := pyB64Decode(val)
		if !ok {
			return "", nil
		}
		val = reverseRunes(s)
	}
	return pyStrip(val), nil
}

func group1(re *regexp.Regexp, hay string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(hay, -1) {
		out = append(out, m[1])
	}
	return out
}

// getText reproduces bs4's get_text(" ", strip=True): every descendant text
// node, individually stripped, empties dropped, joined with one space.
//
// text inside <script>, <style> and <template> is excluded, and so are
// comments: bs4 wraps those in NavigableString subclasses and its type filter
// is an exact `is not` check, so a subclass never qualifies. goquery's .Text()
// does none of this — it concatenates every text node raw, comments aside.
func getText(s *goquery.Selection) string {
	var parts []string
	for _, n := range s.Nodes {
		collectText(n, &parts)
	}
	return strings.Join(parts, " ")
}

func collectText(n *html.Node, out *[]string) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if t := pyStrip(c.Data); t != "" {
				*out = append(*out, t)
			}
		case html.ElementNode:
			switch c.Data {
			case "script", "style", "template":
				continue
			}
			collectText(c, out)
		}
	}
}

// nodeString ports bs4's `el.string or el.get_text()`, which is how the
// embedded_json path reads a <script> body. Tag.string is the lone child when
// there is exactly one, recursing through single-child elements; get_text()
// returns "" for a script because of the type filter above.
func nodeString(s *goquery.Selection) string {
	n := s.Get(0)
	for n != nil {
		if n.FirstChild == nil || n.FirstChild != n.LastChild {
			return getText(s)
		}
		c := n.FirstChild
		if c.Type == html.TextNode {
			return c.Data
		}
		if c.Type != html.ElementNode {
			return getText(s)
		}
		n = c
	}
	return getText(s)
}

// pyB64Decode mirrors base64.b64decode(validate=False) followed by a strict
// utf-8 decode: characters outside the alphabet are discarded rather than
// rejected, padding is still required, and invalid utf-8 fails outright
// instead of being replaced.
func pyB64Decode(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z',
			c >= '0' && c <= '9', c == '+', c == '/', c == '=':
			b.WriteByte(c)
		}
	}
	raw, err := base64.StdEncoding.DecodeString(b.String())
	if err != nil || !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
}

// reverseRunes is python's s[::-1], which walks code points.
func reverseRunes(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
