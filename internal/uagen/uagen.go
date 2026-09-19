// package uagen builds useragents.json: hundreds of realistic current user
// agent strings with share weights, regenerated every run from live version
// apis and usage-share tables.
//
// inputs: chrome-for-testing + chromiumdash (chrome builds), mozilla
// product-details (firefox), edge update api, endoflife.date (ios/macos),
// mdn browser-compat-data (release dates, safari<->webkit mapping),
// whatismybrowser guides (ground-truth templates), statcounter (share
// weights), uap-core test corpus (extra seeds).
//
// every input fetch is best-effort: a failed one warns and its browser
// family drops out, the rest still generate.
package uagen

import (
	"context"
	"time"
)

// fetchTimeout caps one input download. payloads are kb-sized; nothing here
// should take longer than this.
const fetchTimeout = 30 * time.Second

// generateTimeout caps the whole run. inputs fetch serially and a black-holed
// host burns retries + backoff per input, so without this a few throttled
// hosts could eat the pipeline's budget. expired inputs warn and skip.
const generateTimeout = 3 * time.Minute

// Record is one useragents.json entry.
type Record struct {
	UA             string  `json:"ua"`
	Browser        string  `json:"browser"`
	BrowserVersion string  `json:"browser_version"`
	OS             string  `json:"os"`
	OSVersion      string  `json:"os_version"`
	Device         string  `json:"device"`
	Share          float64 `json:"share"`
	VersionDate    string  `json:"version_release_date"`
	GeneratedAt    string  `json:"generated_at"`
	TemplateSource string  `json:"template_source"`
}

// Generate fetches every input and composes the pool. warnings lists the
// inputs that failed and were skipped.
func Generate(ctx context.Context, now time.Time) ([]Record, []string) {
	ctx, cancel := context.WithTimeout(ctx, generateTimeout)
	defer cancel()
	f := &fetcher{now: now}
	v := f.versions(ctx)
	// shares before scrapes: weights matter more than extra templates, so a
	// late expiry drops seeds first
	s := f.shares(ctx)
	recs := compose(v, f.wimb(ctx), f.uacore(ctx, v), s, now)
	return recs, f.warnings
}
