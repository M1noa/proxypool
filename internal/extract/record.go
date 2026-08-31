package extract

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/M1noa/proxypool/internal/config"
)

// Record is one normalized proxy. history and output add their own fields in
// their own packages; the checker's fields live here because internal/output
// needs them alongside everything a source alone can produce.
type Record struct {
	IP           string
	IPVersion    string
	Port         int
	Protocols    []string
	Country      string
	CountryName  string
	Anonymity    string
	HTTPS        bool
	ResponseTime *float64
	Sources      []string
	SourceMeta   map[string]any

	// fields the source vouches for, so the checker leaves them alone
	Provided map[string]bool

	// filled by internal/check
	LastChecked       *string
	ResponseTimeMS    *int
	ResponseTimeRawMS *int
}

// Key is the ip:port identity records are merged and stored under.
func (r *Record) Key() string {
	return r.IP + ":" + strconv.Itoa(r.Port)
}

// NormRecord ports lib/parse._norm_record. it returns nil where python
// returns None: a bogus host, a localhost name, or a port that will not parse.
//
// exported because internal/flows builds its raw maps by hand and normalizes
// them through here.
func NormRecord(raw map[string]any, src *config.Source, defaultProto string) *Record {
	ip := PyStrip(strOr(raw["ip"]))
	// a combined "ip:port" field. one colon only, so ipv6 is left alone.
	if !Truthy(raw["port"]) && strings.Count(ip, ":") == 1 {
		host, port, _ := strings.Cut(ip, ":")
		ip = PyStrip(host)
		raw["port"] = PyStrip(port)
	}
	low := strings.ToLower(ip)
	if low == "localhost" || strings.HasSuffix(low, ".localhost") {
		return nil
	}
	if ip != "" && isBogusIP(ip) {
		return nil
	}
	port, err := strconv.Atoi(PyStrip(strOr(raw["port"])))
	if err != nil {
		return nil
	}
	if ip == "" || port < 1 || port > 65535 {
		return nil
	}

	rec := &Record{
		IP:         ip,
		IPVersion:  ipVersionOf(ip),
		Port:       port,
		Protocols:  []string{},
		Sources:    []string{src.Name},
		SourceMeta: map[string]any{},
	}

	// protocols
	var protos []string
	if p := raw["protocols"]; Truthy(p) {
		switch t := p.(type) {
		case string:
			for _, x := range reProtosSplit.Split(t, -1) {
				protos = append(protos, parseProtocol(x)...)
			}
		case []any:
			for _, x := range t {
				protos = append(protos, parseProtocol(x)...)
			}
		}
	} else if p := raw["protocol"]; Truthy(p) {
		protos = append(protos, parseProtocol(p)...)
	}
	if len(protos) == 0 && defaultProto != "" {
		protos = []string{defaultProto}
	}
	slices.Sort(protos)
	rec.Protocols = slices.Compact(protos)
	if rec.Protocols == nil {
		rec.Protocols = []string{}
	}

	// country. rune counts, not byte counts: python's len() and isalpha() are
	// both unicode-aware, so a two-character non-latin string lands in the
	// iso-code branch there too.
	code := PyStrip(strOr(raw["country"]))
	name := PyStrip(strOr(raw["country_name"]))
	if utf8.RuneCountInString(code) == 2 && isAlpha(code) {
		rec.Country = strings.ToUpper(code)
	} else if code != "" && name == "" {
		// a full name landed in the country slot
		name = code
	}
	rec.CountryName = PyStrip(name)
	if rec.Country == "" && src.Country != "" {
		rec.Country = firstRunes(strings.ToUpper(src.Country), 2)
	}

	// anonymity / https / response time
	anon := raw["anonymity"]
	if !Truthy(anon) {
		anon = src.Anonymity
	}
	rec.Anonymity = normalizeAnon(anon)
	rec.HTTPS = toBool(raw["https"])
	if rt, ok := pyNumber(raw["response_time"]); ok && rt > 0 {
		if src.SpeedUnit == "s" {
			rt *= 1000 // upstream reports seconds, store milliseconds
		}
		rec.ResponseTime = &rt
	}

	// extra metadata, nothing dropped. only None and "" are filtered, so a
	// zero or a false survives.
	for k, v := range raw {
		if !strings.HasPrefix(k, "meta_") || v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		rec.SourceMeta[k[len("meta_"):]] = v
	}

	rec.Provided = map[string]bool{}
	for _, f := range src.Includes {
		rec.Provided[f] = true
	}
	return rec
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

// firstRunes is python's s[:n], which slices characters rather than bytes.
func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
