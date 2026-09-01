package output

import (
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

// rtGoodMS/rtBadMS mirror internal/history's private rtGoodMS/rtBadMS - the
// speed-factor curve used by the sources table's quality score needs the
// same two constants, but they're unexported there so they're duplicated
// here rather than exported cross-package.
const (
	rtGoodMS = 500.0
	rtBadMS  = 8000.0
	// volCapAlive is the alive count that earns full marks on the quality
	// score's volume factor. log-scaled so the jump from 10 to 100 alive
	// proxies matters far more than 900 to 1000.
	//
	// it stays low deliberately. the largest sources here are unchecked
	// aggregators — gfpcom publishes 484k addresses to land 2193 alive, a 0.45%
	// hit rate — so raising the cap would pay them for dump size and dock every
	// curated source. saturating at 1000 means their raw volume earns exactly
	// what a verified 1000 does, and the success-rate term then separates them.
	volCapAlive = 1000.0
)

// selfSource is this repo's own published output, re-fetched every run so a
// proxy no upstream happened to republish this hour is not dropped. it is the
// only source in the table whose input arrives pre-verified, which is what the
// success-rate branch in sourcesTable turns on.
const selfSource = "proxypool"

const cellStyle = `style="border:1px solid #30363d; padding:3px 8px; text-align:left"`

var (
	totalBadgeRe = regexp.MustCompile(`total%20proxies-\d+`)
	avgBadgeRe   = regexp.MustCompile(`avg%20response-[\d.]+ms`)
	// re2 has no lookahead, so python's (?=-green) becomes a captured,
	// re-emitted literal instead of an assertion.
	lastCheckBadgeRe = regexp.MustCompile(`(last%20check-)[\d-]+?(-green)`)
)

// UpdateReadme is write_outputs' README half: refreshes the three summary
// badges and the six marker-delimited HTML tables, then reorders them
// (sources table first, the rest in a flex row). fetchedPerSource/sources
// nil skips the sources table, matching write_outputs' `fetched_per_source
// is not None` gate. now overrides the last-check date for tests; the zero
// value means "use time.Now()".
func UpdateReadme(path string, records []*Record, fetchedPerSource map[string]int, sources []config.Source, now time.Time) error {
	orig, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	t := string(orig)

	total := len(records)
	sum, n := 0, 0
	for _, r := range records {
		if r.ResponseTimeMS != nil && *r.ResponseTimeMS != 0 {
			sum += *r.ResponseTimeMS
			n++
		}
	}
	avg := 0
	if n > 0 {
		avg = pyfmt.Round(float64(sum) / float64(n))
	}
	if now.IsZero() {
		now = time.Now()
	}
	esc := strings.ReplaceAll(now.UTC().Format("2006-01-02"), "-", "--")

	t = totalBadgeRe.ReplaceAllString(t, "total%20proxies-"+strconv.Itoa(total))
	t = avgBadgeRe.ReplaceAllString(t, "avg%20response-"+strconv.Itoa(avg)+"ms")
	t = lastCheckBadgeRe.ReplaceAllString(t, "${1}"+esc+"${2}")

	types := newCounter()
	for _, r := range records {
		it := r.IPType
		if it == "" {
			it = "unknown"
		}
		types.add(it)
	}
	t = replaceMarkerBlock(t, "types", htmlTable([]string{"type", "proxies"}, countRows(types.mostCommon())))

	countries := newCounter()
	for _, r := range records {
		c := r.Country
		if c == "" {
			c = "??"
		}
		countries.add(c)
	}
	ranked := countries.mostCommon()
	top := ranked
	if len(top) > 4 {
		top = top[:4]
	}
	crows := countRows(top)
	// both slices come off the same frequency-ordered list, so "other" is
	// exactly the countries top left out.
	other := 0
	for _, p := range ranked[len(top):] {
		other += p.n
	}
	if other > 0 {
		crows = append(crows, []string{"other", strconv.Itoa(other)})
	}
	t = replaceMarkerBlock(t, "countries", htmlTable([]string{"country", "proxies"}, crows))

	if fetchedPerSource != nil {
		t = replaceMarkerBlock(t, "sources", sourcesTable(records, fetchedPerSource, sources))
	}

	anon := newCounter()
	for _, r := range records {
		a := r.Anonymity
		if a == "" {
			a = "unknown"
		}
		anon.add(a)
	}
	t = replaceMarkerBlock(t, "anon", htmlTable([]string{"anonymity", "proxies"}, countRows(anon.mostCommon())))

	proto := newCounter()
	for _, r := range records {
		for _, p := range r.Protocols {
			proto.add(p)
		}
	}
	t = replaceMarkerBlock(t, "proto", htmlTable([]string{"type", "proxies"}, countRows(proto.mostCommon())))

	ports := newCounter()
	for _, r := range records {
		ports.add(strconv.Itoa(r.Port))
	}
	pc := ports.mostCommon()
	if len(pc) > 5 {
		pc = pc[:5]
	}
	t = replaceMarkerBlock(t, "ports", htmlTable([]string{"port", "proxies"}, countRows(pc)))

	t = layoutBlocks(t)

	return os.WriteFile(path, []byte(t), 0o644)
}

// countPair is one Counter entry: a key and its running count.
type countPair struct {
	key string
	n   int
}

// counter is python's collections.Counter: insertion-ordered counting, with
// mostCommon() stable-sorting by count descending so ties keep insertion
// order, same as python's sorted().
type counter struct {
	order []string
	count map[string]int
}

func newCounter() *counter {
	return &counter{count: map[string]int{}}
}

func (c *counter) add(k string) {
	if _, ok := c.count[k]; !ok {
		c.order = append(c.order, k)
	}
	c.count[k]++
}

func (c *counter) mostCommon() []countPair {
	pairs := make([]countPair, len(c.order))
	for i, k := range c.order {
		pairs[i] = countPair{k, c.count[k]}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].n > pairs[j].n })
	return pairs
}

func countRows(pairs []countPair) [][]string {
	rows := make([][]string, len(pairs))
	for i, p := range pairs {
		rows[i] = []string{p.key, strconv.Itoa(p.n)}
	}
	return rows
}

// htmlTable ports _html_table: one inline-styled table, no internal
// newlines, headers bolded.
func htmlTable(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<table style="border-collapse:collapse; font-size:13px"><thead><tr>`)
	for _, h := range headers {
		b.WriteString("<th " + cellStyle + "><b>" + h + "</b></th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, c := range row {
			b.WriteString("<td " + cellStyle + ">" + c + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// replaceMarkerBlock replaces the text between "<!-- name:start -->\n" and
// "\n<!-- name:end -->" with inner, leaving both markers in place. Uses
// strings.Index slicing rather than regexp.ReplaceAllString because inner
// is built from source/country/org names outside this program's control,
// and a literal "$" in a regexp replacement string is a group reference -
// slicing can't misinterpret content that way. A missing marker pair is a
// no-op, matching python's re.sub silently doing nothing.
func replaceMarkerBlock(t, name, inner string) string {
	startTag := "<!-- " + name + ":start -->"
	endTag := "<!-- " + name + ":end -->"
	si := strings.Index(t, startTag)
	if si == -1 {
		return t
	}
	ei := strings.Index(t[si:], endTag)
	if ei == -1 {
		return t
	}
	ei += si
	return t[:si] + startTag + "\n" + inner + "\n" + t[ei:]
}

// layoutBlocks ports write_outputs' final splice: pull each of the six
// marker blocks (markers included) out of the already-updated text, then
// rebuild the region spanning all of them as the sources table followed by
// the rest side-by-side in a flex div. Text before the first marker and
// after the last marker (whatever it was) passes through unchanged - this
// is also what makes a stray trailing tag from a prior run's flex div
// accumulate on every run; that's an existing behavior of the splice
// itself, not something this port needs to special-case.
func layoutBlocks(t string) string {
	order := []string{"sources", "types", "countries", "anon", "proto", "ports"}
	parts := make(map[string]string, len(order))
	var starts, ends []int
	for _, name := range order {
		startTag := "<!-- " + name + ":start -->"
		si := strings.Index(t, startTag)
		if si == -1 {
			parts[name] = ""
			continue
		}
		endTag := "<!-- " + name + ":end -->"
		rel := strings.Index(t[si:], endTag)
		if rel == -1 {
			parts[name] = ""
			continue
		}
		ei := si + rel + len(endTag)
		parts[name] = t[si:ei]
		starts = append(starts, si)
		ends = append(ends, ei)
	}
	if len(starts) == 0 {
		return t
	}
	minStart, maxEnd := starts[0], ends[0]
	for _, s := range starts[1:] {
		if s < minStart {
			minStart = s
		}
	}
	for _, e := range ends[1:] {
		if e > maxEnd {
			maxEnd = e
		}
	}

	flexOrder := []string{"types", "countries", "anon", "proto", "ports"}
	flexParts := make([]string, len(flexOrder))
	for i, name := range flexOrder {
		flexParts[i] = parts[name]
	}
	flex := `<div style="display:flex; flex-wrap:wrap; gap:16px; align-items:flex-start">` + "\n\n" +
		strings.Join(flexParts, "\n\n") + "\n\n</div>"

	return t[:minStart] + parts["sources"] + "\n\n" + flex + t[maxEnd:]
}

// repoOf ports repo_of: extracts (owner, repo) from a github.com,
// raw.githubusercontent.com, or cdn.jsdelivr.net/gh/ url. Any other host,
// or too few path segments, reports ok=false.
func repoOf(rawURL string) (owner, repo string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", false
	}
	var parts []string
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	switch u.Host {
	case "raw.githubusercontent.com", "github.com":
		if len(parts) > 0 && parts[0] == "wiki" {
			parts = parts[1:]
		}
		if len(parts) >= 2 {
			return parts[0], parts[1], true
		}
	case "cdn.jsdelivr.net":
		if len(parts) >= 3 && parts[0] == "gh" {
			return parts[1], parts[2], true
		}
	}
	return "", "", false
}

// urlHost returns a url's host with a leading "www." stripped, or "" if the
// url is empty/unparseable/hostless.
func urlHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// urlStem is the last path segment of a url with its extension removed -
// python's `url.rsplit("/", 1)[-1].rsplit(".", 1)[0]`.
func urlStem(rawURL string) string {
	last := rawURL
	if i := strings.LastIndex(rawURL, "/"); i != -1 {
		last = rawURL[i+1:]
	}
	if i := strings.LastIndex(last, "."); i != -1 {
		last = last[:i]
	}
	return last
}

// sourceLinkNames ports _source_link_names: for each source, a link (its
// home url, falling back to a repo url, falling back to its bare host) and
// a display label (its display field, falling back to "owner/repo" -
// disambiguated with the url's file stem if that repo has multiple
// sources - falling back to host or the source name).
//
// The label fallback to host/name is INTENTIONALLY only reachable inside
// the `link == ""` branch, matching python exactly: a source with an
// explicit `home` but an unrecognized repo url and no `display` ends up
// with label == "" permanently. That is a genuine quirk of the python this
// ports, not a bug this port should "fix".
//
// order is the source names in first-seen order (python dict insertion
// order), for the caller to fall back to when there are no sources at all.
func sourceLinkNames(sources []config.Source) (home, display map[string]string, order []string) {
	type repoKey struct{ owner, repo string }
	repos := make(map[string]repoKey, len(sources))
	counts := map[repoKey]int{}
	for _, s := range sources {
		if owner, repo, ok := repoOf(s.URL); ok {
			k := repoKey{owner, repo}
			repos[s.Name] = k
			counts[k]++
		}
	}

	home = make(map[string]string, len(sources))
	display = make(map[string]string, len(sources))
	seen := make(map[string]bool, len(sources))
	for _, s := range sources {
		link := s.Home
		label := s.Display
		if r, ok := repos[s.Name]; ok {
			if link == "" {
				link = "https://github.com/" + r.owner + "/" + r.repo
			}
			if label == "" {
				label = r.owner + "/" + r.repo
				if counts[r] > 1 {
					label = label + " (" + urlStem(s.URL) + ")"
				}
			}
		}
		if link == "" {
			host := urlHost(s.URL)
			if host != "" {
				link = "https://" + host
			}
			if label == "" {
				label = host
			}
			if label == "" {
				label = s.Name
			}
		}
		home[s.Name] = link
		display[s.Name] = label
		if !seen[s.Name] {
			seen[s.Name] = true
			order = append(order, s.Name)
		}
	}
	return home, display, order
}

func speedPct(avgRT int) int {
	if avgRT == 0 {
		return 50
	}
	factor := 1.0 - (float64(avgRT)-rtGoodMS)/(rtBadMS-rtGoodMS)
	factor = math.Max(0, math.Min(1, factor))
	return pyfmt.Round(100 * factor)
}

// reliabilityPct ports _reliability_pct: rel in [0,1]. callers with no history
// to average pass the neutral 0.5.
func reliabilityPct(rel float64) int {
	return pyfmt.Round(100 * rel)
}

// volumePct rewards a source for how many alive proxies it actually
// contributed, so a source with a great success rate on 2 proxies no longer
// scores the same as one with the same rate on 2000.
func volumePct(alive int) int {
	if alive <= 0 {
		return 0
	}
	factor := math.Min(1, math.Log1p(float64(alive))/math.Log1p(volCapAlive))
	return pyfmt.Round(100 * factor)
}

// sourceAgg keeps running sums rather than the samples. both slices existed only
// to be averaged, and they grew one entry per (record, source) pair — plus, for
// rels, one heap-boxed float64 per record — to produce two numbers.
type sourceAgg struct {
	alive     int
	rtSum     int
	rtN       int
	relSum    float64
	relN      int
	countries *counter
}

// sourcesTable ports write_outputs' per-source table: aggregates fresh
// (non-carried) records by source, blends success rate/speed/reliability/
// volume into a quality score, sorts by (quality, alive) descending, and
// bolds the #1 row.
//
// the weights are 0.25 success, 0.25 speed, 0.15 reliability, 0.35 volume.
// success rate carried 0.40 and volume 0.20, which let a 57-address list at 89%
// alive outrank a 3052-address one at 36% — a rate on a tiny sample is both
// noisier and more flattering than the same rate on thousands, and the score was
// paying for that twice. success keeps a quarter of the weight because it is the
// only term that tells a curated list apart from an unchecked aggregator.
func sourcesTable(records []*Record, fetchedPerSource map[string]int, sources []config.Source) string {
	agg := map[string]*sourceAgg{}
	for _, r := range records {
		if r.Carried {
			continue
		}
		for _, s := range r.Sources {
			a, ok := agg[s]
			if !ok {
				a = &sourceAgg{countries: newCounter()}
				agg[s] = a
			}
			a.alive++
			if r.ResponseTimeMS != nil && *r.ResponseTimeMS != 0 {
				a.rtSum += *r.ResponseTimeMS
				a.rtN++
			}
			if r.Check != nil {
				a.relSum += r.Check.Reliability
				a.relN++
			}
			if r.Country != "" {
				a.countries.add(r.Country)
			}
		}
	}

	home, display, order := sourceLinkNames(sources)
	names := order
	if len(names) == 0 {
		for k := range fetchedPerSource {
			names = append(names, k)
		}
		sort.Strings(names)
	}

	type row struct {
		quality, alive, fetched, pct, relPct, avgRT int
		name, topCountries                          string
	}
	rows := make([]row, 0, len(names))
	for _, s := range names {
		fetched := fetchedPerSource[s]
		a := agg[s]
		// a source that fetched but landed nothing alive has no agg entry
		if a == nil {
			a = &sourceAgg{countries: newCounter()}
		}
		alive := a.alive

		pct := 0
		if fetched != 0 {
			pct = pyfmt.Round(100 * float64(alive) / float64(fetched))
		}
		// the success column means "of the addresses this source handed us, how
		// many answered" — a discovery hit rate. selfSource is the one row where
		// that division measures something else: its input is this repo's own
		// previous output, every address in it already probed alive, so alive over
		// fetched is retention across one run interval rather than discovery.
		//
		// two honest numbers exist for that row and they bracket the truth: 100%,
		// alive when published, and the retention rate, still alive an interval
		// later. neither is the discovery rate the column asks for, because this
		// source does no discovery. the midpoint is printed instead — an interval
		// estimate, not a measurement, which is the reason it is spelled out here
		// rather than left to look like the rate above it.
		if s == selfSource {
			pct = pyfmt.Round(float64(100+pct) / 2)
		}

		avgRT := 0
		if a.rtN > 0 {
			avgRT = pyfmt.Round(float64(a.rtSum) / float64(a.rtN))
		}

		rel := 0.5
		if a.relN > 0 {
			rel = a.relSum / float64(a.relN)
		}

		relPct := reliabilityPct(rel)

		speed := speedPct(avgRT)
		volume := volumePct(alive)
		quality := pyfmt.Round(0.25*float64(pct) + 0.25*float64(speed) + 0.15*float64(relPct) + 0.35*float64(volume))

		top := a.countries.mostCommon()
		if len(top) > 2 {
			top = top[:2]
		}
		names := make([]string, len(top))
		for i, p := range top {
			names[i] = p.key
		}
		topCountries := strings.Join(names, ", ")
		if topCountries == "" {
			topCountries = "?"
		}

		rows = append(rows, row{quality, alive, fetched, pct, relPct, avgRT, s, topCountries})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].quality != rows[j].quality {
			return rows[i].quality > rows[j].quality
		}
		return rows[i].alive > rows[j].alive
	})

	srows := make([][]string, len(rows))
	for i, x := range rows {
		label := display[x.name]
		cell := label
		if link := home[x.name]; link != "" {
			cell = `<a href="` + link + `">` + label + `</a>`
		}
		if i == 0 {
			cell = "<b>" + cell + "</b>"
		}
		srows[i] = []string{
			cell,
			strconv.Itoa(x.quality),
			strconv.Itoa(x.pct) + "%",
			strconv.Itoa(x.relPct) + "%",
			strconv.Itoa(x.avgRT) + "ms",
			strconv.Itoa(x.fetched),
			strconv.Itoa(x.alive),
			x.topCountries,
		}
	}
	return htmlTable(
		[]string{"source", "quality", "success", "reliability", "avg rt", "fetched", "alive", "top countries"},
		srows,
	)
}
