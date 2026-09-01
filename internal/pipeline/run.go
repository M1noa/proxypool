package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/M1noa/proxypool/internal/check"
	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/fetch"
	"github.com/M1noa/proxypool/internal/flows"
	"github.com/M1noa/proxypool/internal/geoip"
	"github.com/M1noa/proxypool/internal/history"
	"github.com/M1noa/proxypool/internal/memwatch"
	"github.com/M1noa/proxypool/internal/output"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

// DefaultBudget is TIME_BUDGET_S: a hard wall-clock cap on the whole run,
// under the workflow's 2h actions timeout.
const DefaultBudget = time.Duration(1.95 * float64(time.Hour))

// Options is the full pipeline's configuration. the zero value runs everything
// against ./sources.jsonc, ./output and ./README.md.
type Options struct {
	Root        string // defaults to "."
	Out         string // defaults to <root>/output
	Cache       string // defaults to <root>/.cache
	SourcesPath string // defaults to <root>/sources.jsonc
	Readme      string // defaults to <root>/README.md

	SkipFetch     bool // reuse the previous proxies.json instead of fetching
	SkipCheck     bool // fetch and merge only: no geoip, asn, history or probing
	SkipSpeedtest bool // derive concurrency without measuring bandwidth
	SkipGeoIP     bool
	SkipASN       bool
	SkipHistory   bool
	SkipReadme    bool

	Only    []string // only these source names
	Exclude []string // drop these source names
	Formats []string // only sources with one of these formats

	Limit       int // cap on records carried into the check phase
	Concurrency int // 0 derives from cpu, ram and bandwidth
	Timeout     time.Duration
	Budget      time.Duration // 0 uses DefaultBudget

	DryRun bool
	Logf   func(format string, args ...any)
}

func (o *Options) fill() {
	if o.Root == "" {
		o.Root = "."
	}
	if o.Out == "" {
		o.Out = filepath.Join(o.Root, "output")
	}
	if o.Cache == "" {
		o.Cache = filepath.Join(o.Root, ".cache")
	}
	if o.SourcesPath == "" {
		o.SourcesPath = filepath.Join(o.Root, "sources.jsonc")
	}
	if o.Readme == "" {
		o.Readme = filepath.Join(o.Root, "README.md")
	}
	if o.Budget == 0 {
		o.Budget = DefaultBudget
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// Run is fetch_proxies.py main().
func Run(ctx context.Context, o Options) error {
	o.fill()
	logf := o.Logf
	tRun := time.Now()

	// silent unless the process crosses half of system ram, which a healthy run
	// does not come close to. when it does speak, it says where the memory is
	// before it tries to reclaim any.
	watch := &memwatch.Watcher{Logf: logf}
	defer watch.Start(ctx)()

	all, err := config.Load(o.SourcesPath)
	if err != nil {
		return err
	}
	sources := filterSources(all, o)

	var raw []*extract.Record
	var errs []string
	if !o.SkipFetch {
		t0 := time.Now()
		pool := &fetch.Pool{F: &fetch.Fetcher{Flows: flows.Table}, Logf: logf}
		var stats map[string]fetch.Stat
		raw, errs, stats = pool.Run(ctx, pointers(sources))
		logf("")
		logf("fetched %d raw records from %d sources in %.1fs", len(raw), len(sources), time.Since(t0).Seconds())
		logSlowest(stats, logf)
	}

	records := finalize(merge(raw))
	logf("unique proxies: %d", len(records))

	// per-source fetched counts, taken before dead proxies are dropped so the
	// readme's success rate has a denominator
	fetchedPerSource := map[string]int{}
	items := make([]*item, 0, len(records))
	seen := make(map[string]bool, len(records))
	for _, r := range records {
		for _, s := range r.Sources {
			fetchedPerSource[s]++
		}
		items = append(items, &item{rec: r})
		seen[r.Key()] = true
	}

	prev, existed, err := loadPrevious(filepath.Join(o.Out, "proxies.json"), seen)
	if err != nil {
		return err
	}
	if existed {
		items = append(items, prev...)
		logf("carried over %d proxies from previous run", len(prev))
	}

	if o.Limit > 0 && len(items) > o.Limit {
		items = items[:o.Limit]
		logf("limit: kept %d records", len(items))
	}

	if !o.SkipCheck {
		if items, err = checkPhase(ctx, o, items, tRun, watch); err != nil {
			return err
		}
	}

	t0 := time.Now()
	n, err := writeOutputs(o, items, fetchedPerSource, sources)
	if err != nil {
		return err
	}
	logf("wrote %d proxies to proxies.json (in %.1fs)", n, time.Since(t0).Seconds())
	logf("total elapsed: %.1fs", time.Since(tRun).Seconds())
	if len(errs) > 0 {
		logf("")
		logf("errors:")
		for _, e := range errs {
			logf("  %s", e)
		}
	}
	return nil
}

// checkPhase fills in country, asn and ip_type, probes every record's claimed
// protocols, and folds the resulting history into per-record scores. it returns
// only the records that answered.
func checkPhase(ctx context.Context, o Options, items []*item, tRun time.Time, watch *memwatch.Watcher) ([]*item, error) {
	logf := o.Logf

	if !o.SkipGeoIP {
		if err := fillCountries(ctx, o, items); err != nil {
			return nil, err
		}
	}
	if !o.SkipASN {
		if err := fillASN(ctx, o, items); err != nil {
			return nil, err
		}
	}

	recs := make([]*extract.Record, len(items))
	byRec := make(map[*extract.Record]*item, len(items))
	for i, it := range items {
		recs[i] = it.rec
		byRec[it.rec] = it
	}

	var hist *history.History
	var state map[history.Key]history.State
	var skips map[check.SkipKey]bool
	var prevAlive map[check.AliveKey]bool
	if !o.SkipHistory {
		h, err := history.Open(filepath.Join(o.Out, "history.duckdb"))
		if err != nil {
			return nil, err
		}
		hist = h
		// duckdb's memory is cgo memory, invisible to runtime.MemStats, so the
		// watchdog has to ask duckdb itself. h, not hist: hist is cleared below
		// once history is done with, and h's own handle reports a closed database
		// as no reading rather than a crash.
		watch.Probe(h.MemoryUsage)
		defer func() {
			if hist != nil {
				hist.Close()
			}
		}()
		if state, err = hist.StateMap(); err != nil {
			return nil, err
		}
		skips = history.SkipKeys(recs, state, time.Time{}, nil)
		prevAlive = history.AliveMap(state)
		if len(skips) > 0 {
			logf("skipping %d dead protocol probes (re-checked in ~24h)", len(skips))
		}
	}

	logf("checking %d proxies...", len(recs))
	t0 := time.Now()
	cctx, cancel := context.WithDeadline(ctx, tRun.Add(o.Budget))
	alive, stats, outcomes := check.CheckAll(cctx, recs, check.Options{
		Concurrency: o.Concurrency,
		Skip:        skips,
		Speedtest:   !o.SkipSpeedtest,
		PrevAlive:   prevAlive,
		Logf:        logf,
		Timeout:     o.Timeout,
	})
	cancel()
	logf("alive=%d dead=%d skipped=%d revived=%d baseline=%dms in %.1fs",
		stats.Alive, stats.Dead, stats.Skipped, stats.Revived, stats.BaselineMS, time.Since(t0).Seconds())

	kept := make([]*item, 0, len(alive))
	for _, r := range alive {
		kept = append(kept, byRec[r])
	}

	if hist != nil {
		t0 := time.Now()
		if err := hist.UpdateState(outcomes, history.Now(), state); err != nil {
			return nil, err
		}
		scores, err := hist.Scores()
		if err != nil {
			return nil, err
		}
		pruned, err := hist.Prune(time.Now())
		if err != nil {
			return nil, err
		}
		if err := hist.Close(); err != nil {
			return nil, err
		}
		hist = nil
		for _, it := range kept {
			it.check = blendScores(it.rec, scores)
		}
		logf("history: %d proto states scored, %d pruned in %.1fs",
			len(scores), pruned, time.Since(t0).Seconds())
	}
	return kept, nil
}

// blendScores averages the record's live protocols' reliability and quality,
// sums their counters, and takes the outer bounds of their timestamps. a record
// with no scored protocol is treated as brand new.
func blendScores(r *extract.Record, scores map[history.Key]history.Score) *output.CheckFields {
	var parts []history.Score
	for _, p := range r.Protocols {
		if s, ok := scores[history.Key{IP: r.IP, Port: r.Port, Proto: p}]; ok {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return &output.CheckFields{Reliability: history.NewProxyScore, Quality: history.NewProxyScore}
	}
	f := &output.CheckFields{}
	var rel, qual float64
	for _, p := range parts {
		rel += p.Reliability
		qual += p.Quality
		f.ChecksTotal += p.ChecksTotal
		f.ChecksOK += p.ChecksOK
		f.FirstSeen = minTS(f.FirstSeen, p.FirstSeen)
		f.LastSeen = maxTS(f.LastSeen, p.LastSeen)
	}
	f.Reliability = pyfmt.RoundN(rel/float64(len(parts)), 4)
	f.Quality = pyfmt.RoundN(qual/float64(len(parts)), 4)
	return f
}

// minTS and maxTS skip nils. python would raise on a mix of None and str, but
// every scored row has both timestamps set, so the only reachable nil case is
// all-nil - which yields nil either way.
func minTS(a, b *string) *string {
	if a == nil {
		return b
	}
	if b == nil || *a <= *b {
		return a
	}
	return b
}

func maxTS(a, b *string) *string {
	if a == nil {
		return b
	}
	if b == nil || *a >= *b {
		return a
	}
	return b
}

func fillCountries(ctx context.Context, o Options, items []*item) error {
	t0 := time.Now()
	path, err := geoip.DownloadMMDB(ctx, o.Cache)
	if err != nil {
		return err
	}
	g, err := geoip.OpenGeoIP(path)
	if err != nil {
		return err
	}
	defer g.Close()

	filled := 0
	for _, it := range items {
		if it.rec.Country != "" {
			continue
		}
		if c := g.Country(it.rec.IP); c != "" {
			it.rec.Country = c
			filled++
		}
	}
	o.Logf("geoip filled country for %d records in %.1fs", filled, time.Since(t0).Seconds())
	return nil
}

func fillASN(ctx context.Context, o Options, items []*item) error {
	t0 := time.Now()
	categories, err := geoip.DownloadASNCategories(ctx, o.Cache)
	if err != nil {
		return err
	}
	o.Logf("ipverse asn categories: %d classified", len(categories))
	path, err := geoip.DownloadASNMMDB(ctx, o.Cache)
	if err != nil {
		return err
	}
	db, err := geoip.OpenAsnDB(path, categories)
	if err != nil {
		return err
	}
	defer db.Close()

	for _, it := range items {
		info := db.Lookup(it.rec.IP)
		it.asn, it.asOrg, it.ipType = info.ASN, info.AsOrg, info.IPType
	}
	o.Logf("asn/ip_type filled in %.1fs", time.Since(t0).Seconds())
	return nil
}

// writeOutputs is write_outputs: sort, serialize, and refresh the readme.
func writeOutputs(o Options, items []*item, fetchedPerSource map[string]int, sources []config.Source) (int, error) {
	recs := make([]*output.Record, len(items))
	for i, it := range items {
		r := output.FromRecord(it.rec)
		r.Carried = it.carried
		r.ASN, r.AsOrg, r.IPType = it.asn, it.asOrg, it.ipType
		r.Check = it.check
		recs[i] = r
	}
	output.SortRecords(recs)
	if o.DryRun {
		return len(recs), nil
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return 0, err
	}
	if err := writeJSON(filepath.Join(o.Out, "proxies.json"), recs); err != nil {
		return 0, err
	}
	if o.SkipReadme {
		return len(recs), nil
	}
	return len(recs), output.UpdateReadme(o.Readme, recs, fetchedPerSource, sources, time.Time{})
}

// writeJSON streams the array straight to the file. os.WriteFile would want the
// whole few-hundred-megabyte encoding as one []byte, on top of the records it
// was encoded from.
func writeJSON(path string, recs []*output.Record) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := output.EncodeTo(f, recs); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// filterSources applies -sources, -exclude and -format, keeping sources.jsonc's
// own order so the fetch pools and the readme table stay comparable.
func filterSources(all []config.Source, o Options) []config.Source {
	if len(o.Only) == 0 && len(o.Exclude) == 0 && len(o.Formats) == 0 {
		return all
	}
	out := make([]config.Source, 0, len(all))
	for _, s := range all {
		if len(o.Only) > 0 && !slices.Contains(o.Only, s.Name) {
			continue
		}
		if slices.Contains(o.Exclude, s.Name) {
			continue
		}
		if len(o.Formats) > 0 && !slices.Contains(o.Formats, s.Format) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func pointers(sources []config.Source) []*config.Source {
	out := make([]*config.Source, len(sources))
	for i := range sources {
		out[i] = &sources[i]
	}
	return out
}

// logSlowest is main()'s top-10 elapsed table. python breaks ties on
// sources.jsonc order, which a go map cannot preserve, so ties go by name -
// this is a stdout line only, nothing in the outputs reads it.
func logSlowest(stats map[string]fetch.Stat, logf func(string, ...any)) {
	type entry struct {
		name string
		st   fetch.Stat
	}
	all := make([]entry, 0, len(stats))
	for name, st := range stats {
		all = append(all, entry{name, st})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].st.Elapsed != all[j].st.Elapsed {
			return all[i].st.Elapsed > all[j].st.Elapsed
		}
		return all[i].name < all[j].name
	})
	if len(all) > 10 {
		all = all[:10]
	}
	logf("slowest sources:")
	for _, e := range all {
		logf("  %-24s %5.1fs  %7d recs  %d reqs",
			e.name, e.st.Elapsed.Seconds(), e.st.Fetched, e.st.Requests)
	}
}
