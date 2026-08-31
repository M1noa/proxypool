// package config reads sources.jsonc: the comment-stripping loader in jsonc.go
// and the source structs here, including the string-or-object Spec union each
// field extractor is written as.
package config

import (
	"encoding/json"
	"fmt"
)

// Spec is a single field extractor. sources.jsonc writes it either as a bare
// string — a dotted json path, or a css selector — or as an object carrying
// modifiers. mirrors the isinstance(spec, dict) branches in lib/parse.py.
type Spec struct {
	raw    string
	object bool

	// json object form: {"path": "a.b", "map": {"1": "http"}}
	Path string
	Map  map[string]any

	// html object form: {"selector": "td", "attr": …, "decode": …, "regex": …}
	Selector string
	Attr     string
	Decode   string
	Regex    string

	// html const form. no source in sources.jsonc uses it; kept because
	// _extract_html branches on it before touching the selector.
	Const    string
	HasConst bool
}

func (s *Spec) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.raw)
	}
	var o struct {
		Path     string          `json:"path"`
		Map      map[string]any  `json:"map"`
		Selector string          `json:"selector"`
		Attr     string          `json:"attr"`
		Decode   string          `json:"decode"`
		Regex    string          `json:"regex"`
		Const    json.RawMessage `json:"const"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	s.object = true
	s.Path, s.Map = o.Path, o.Map
	s.Selector, s.Attr, s.Decode, s.Regex = o.Selector, o.Attr, o.Decode, o.Regex
	if len(o.Const) > 0 {
		s.HasConst = true
		if err := json.Unmarshal(o.Const, &s.Const); err != nil {
			// non-string const: keep the raw json, python would pass it through
			s.Const = string(o.Const)
		}
	}
	return nil
}

// Empty reports whether python's `if not spec` would have skipped this field.
// an absent key, an empty string and an empty object are all falsy there.
func (s Spec) Empty() bool {
	if s.object {
		return s.Path == "" && s.Map == nil && s.Selector == "" &&
			s.Attr == "" && s.Decode == "" && s.Regex == "" && !s.HasConst
	}
	return s.raw == ""
}

// IsObject reports whether the source wrote the object form.
func (s Spec) IsObject() bool { return s.object }

// JSONPath is the dotted path to dig, for json extraction.
func (s Spec) JSONPath() string {
	if s.object {
		return s.Path
	}
	return s.raw
}

// CSSSelector is the selector to match within a row, for html extraction.
// empty means "use the row element itself".
func (s Spec) CSSSelector() string {
	if s.object {
		return s.Selector
	}
	return s.raw
}

// HasMap distinguishes a present-but-empty map from an absent one, matching
// python's `if spec.get("map") is not None`.
func (s Spec) HasMap() bool { return s.object && s.Map != nil }

// Embedded describes a json blob carried inside a <script> tag.
type Embedded struct {
	Selector string `json:"selector"`
	Regex    string `json:"regex"`
}

// Extract holds the per-format extraction rules under a source's "extract".
type Extract struct {
	// text
	Regex    string `json:"regex"`
	Finditer bool   `json:"finditer"`

	// json
	Root string `json:"root"`

	// html
	RowSelector  string    `json:"row_selector"`
	EmbeddedJSON *Embedded `json:"embedded_json"`

	// field specs
	IP           Spec `json:"ip"`
	Port         Spec `json:"port"`
	Protocol     Spec `json:"protocol"`
	Protocols    Spec `json:"protocols"`
	Country      Spec `json:"country"`
	CountryName  Spec `json:"country_name"`
	Anonymity    Spec `json:"anonymity"`
	HTTPS        Spec `json:"https"`
	ResponseTime Spec `json:"response_time"`

	SourceMeta map[string]Spec `json:"source_meta"`
}

// EffectiveExtract returns the extraction rules with a top-level source_meta
// folded in. proxifly, geonode, nodemaven, roundproxies, proxiware and
// proxyscrape declare theirs at the source level, and lib/parse.py only ever
// read extract.source_meta, so their city/isp/org/score metadata never reached
// the output. extract.source_meta wins on a key collision; no source declares
// both today.
func (s *Source) EffectiveExtract() *Extract {
	if len(s.SourceMeta) == 0 {
		return &s.Extract
	}
	ex := s.Extract
	ex.SourceMeta = make(map[string]Spec, len(s.SourceMeta)+len(s.Extract.SourceMeta))
	for k, v := range s.SourceMeta {
		ex.SourceMeta[k] = v
	}
	for k, v := range s.Extract.SourceMeta {
		ex.SourceMeta[k] = v
	}
	return &ex
}

// RowSel is the row selector with python's "tr" default applied.
func (e Extract) RowSel() string {
	if e.RowSelector == "" {
		return "tr"
	}
	return e.RowSelector
}

// Pagination templates {page}/{offset} into a source url.
//
// Type and Stop are parsed but never read — dead config keys retained so a
// strict unmarshal wouldn't reject the file. stop-on-empty is unconditional.
type Pagination struct {
	Start     *int   `json:"start"`
	Step      *int   `json:"step"`
	MaxPages  *int   `json:"max_pages"`
	DelayMS   int    `json:"delay_ms"`
	TotalPath string `json:"total_path"`
	PagesPath string `json:"pages_path"`
	Limit     *int   `json:"limit"`
	Type      string `json:"type"`
	Stop      string `json:"stop"`
}

func deref(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func (p Pagination) StartAt() int { return deref(p.Start, 1) }
func (p Pagination) StepBy() int  { return deref(p.Step, 1) }
func (p Pagination) PageSize() int {
	return deref(p.Limit, 100)
}

// MaxPagesOr applies the caller's default when the source omits max_pages.
func (p Pagination) MaxPagesOr(def int) int { return deref(p.MaxPages, def) }

// PrefetchStep discovers a token or a real url before the main fetch.
type PrefetchStep struct {
	URL      string `json:"url"`
	Method   string `json:"method"`
	Body     any    `json:"body"`
	BodyType string `json:"body_type"`
	Regex    string `json:"regex"`
	Group    *int   `json:"group"`
	JSONPath string `json:"json_path"`
	Header   string `json:"header"`
	AsURL    bool   `json:"as_url"`
	Base     string `json:"base"`
}

func (s PrefetchStep) GroupNum() int { return deref(s.Group, 1) }

// URLEntry is one element of a source's "urls" array: a url plus constant
// field values merged underneath whatever the extractor produces.
type URLEntry struct {
	URL string         `json:"url"`
	Set map[string]any `json:"set"`
}

// Source is one entry in sources.jsonc.
type Source struct {
	Name    string `json:"name"`
	Display string `json:"display"`
	Home    string `json:"home"`
	Format  string `json:"format"`

	URL  string     `json:"url"`
	URLs []URLEntry `json:"urls"`
	Flow string     `json:"flow"`

	Extract    Extract           `json:"extract"`
	Pagination *Pagination       `json:"pagination"`
	Prefetch   []PrefetchStep    `json:"prefetch"`
	Headers    map[string]string `json:"headers"`
	Antibot    bool              `json:"antibot"`

	// fields the source vouches for, so the checker leaves them alone
	Includes []string `json:"includes"`

	Protocol  string `json:"protocol"`
	Country   string `json:"country"`
	Anonymity string `json:"anonymity"`

	BudgetS *int `json:"budget_s"`

	// declared at the top level by 6 sources; folded into the extraction rules
	// by EffectiveExtract.
	SourceMeta map[string]Spec `json:"source_meta"`

	// read by lib/util.request and lib/parse.fetch_source but set by no source
	Method      string `json:"method"`
	Body        any    `json:"body"`
	BodyType    string `json:"body_type"`
	FallbackURL string `json:"fallback_url"`
	Timeout     *int   `json:"timeout"`
	SpeedUnit   string `json:"speed_unit"`
}

// Fmt is the format with python's "text" default applied.
func (s Source) Fmt() string {
	if s.Format == "" {
		return "text"
	}
	return s.Format
}

// Budget is the per-source wall-clock budget in seconds.
func (s Source) Budget() int { return deref(s.BudgetS, 80) }

// TimeoutS is the per-request timeout in seconds.
func (s Source) TimeoutS() int { return deref(s.Timeout, 12) }

// HTTPMethod defaults to GET.
func (s Source) HTTPMethod() string {
	if s.Method == "" {
		return "GET"
	}
	return s.Method
}

// Label is the human-facing name used in the readme table.
func (s Source) Label() string {
	if s.Display != "" {
		return s.Display
	}
	return s.Name
}

// Load reads sources.jsonc, whose top level is {"sources": [...]}.
func Load(path string) ([]Source, error) {
	var cfg struct {
		Sources []Source `json:"sources"`
	}
	if err := LoadJSONC(path, &cfg); err != nil {
		return nil, err
	}
	srcs := cfg.Sources
	for i, s := range srcs {
		if s.Name == "" {
			return nil, fmt.Errorf("%s: source %d has no name", path, i)
		}
	}
	return srcs, nil
}
