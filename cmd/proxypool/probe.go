package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/fetch"
	"github.com/M1noa/proxypool/internal/flows"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

// probeResult mirrors tools/probe_endpoints.py's per-source dict, key order
// included, so the two -json outputs diff cleanly.
type probeResult struct {
	Name     string   `json:"name"`
	Format   string   `json:"format"`
	Protocol string   `json:"protocol"`
	Status   string   `json:"status"`
	Count    int      `json:"count"`
	Seconds  float64  `json:"seconds"`
	Errors   []string `json:"errors"`
}

var probeMarks = map[string]string{"ok": ".", "empty": "EMPTY", "error": "ERR", "crash": "CRASH"}

// runProbe is tools/probe_endpoints.py: fetch every source endpoint without
// checking a single proxy, and report which ones are dead, empty or misconfigured.
func runProbe(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	workers := fs.Int("workers", 10, "concurrent sources")
	out := fs.String("json", "", "write full results to this path")
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	sources, err := config.Load(filepath.Join(*root, "sources.jsonc"))
	if err != nil {
		return err
	}
	fmt.Printf("probing %d sources (workers=%d)...\n\n", len(sources), *workers)

	f := &fetch.Fetcher{Flows: flows.Table}
	results := make([]probeResult, len(sources))

	var wg sync.WaitGroup
	var mu sync.Mutex // serializes the progress lines only
	sem := make(chan struct{}, max(1, *workers))
	for i := range sources {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r := probeOne(ctx, f, &sources[i])
			results[i] = r
			mu.Lock()
			fmt.Printf("[%-5s] %-22s %7d  (%.1fs)\n", probeMarks[r.Status], r.Name, r.Count, r.Seconds)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if (a.Status == "ok") != (b.Status == "ok") {
			return a.Status == "ok"
		}
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		return a.Name < b.Name
	})

	var empty, bad []probeResult
	nOK, total := 0, 0
	for _, r := range results {
		total += r.Count
		switch r.Status {
		case "ok":
			nOK++
		case "empty":
			empty = append(empty, r)
		default:
			bad = append(bad, r)
		}
	}

	fmt.Println("\n===== summary =====")
	fmt.Printf("ok=%d empty=%d error/crash=%d total=%d total_proxies=%d\n",
		nOK, len(empty), len(bad), len(results), total)

	if len(empty) > 0 {
		fmt.Println("\nEMPTY (returned 0 records):")
		for _, r := range empty {
			fmt.Printf("  %-22s %-6s %s\n", r.Name, r.Format, r.Protocol)
		}
	}
	if len(bad) > 0 {
		fmt.Println("\nFAILED:")
		for _, r := range bad {
			for _, e := range r.Errors {
				fmt.Printf("  %-22s %s\n", r.Name, e)
			}
		}
	}

	if *out == "" {
		return nil
	}
	var buf []byte
	buf, err = marshalIndentNoEscape(results)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf, 0o644); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s\n", *out)
	return nil
}

// probeOne fetches one source and classifies the result. a panic is caught and
// reported as a crashed source, so one bad extract config cannot take down a
// 150-source sweep - python gets this from its bare `except`.
func probeOne(ctx context.Context, f *fetch.Fetcher, src *config.Source) (r probeResult) {
	r = probeResult{Name: src.Name, Format: src.Fmt(), Protocol: src.Protocol, Errors: []string{}}
	if r.Protocol == "" {
		r.Protocol = "mixed"
	}
	defer func() {
		if p := recover(); p != nil {
			r.Status, r.Count, r.Seconds = "crash", 0, 0
			r.Errors = []string{fmt.Sprintf("crashed: %v", p)}
		}
	}()

	t0 := time.Now()
	recs, errs := f.Source(ctx, src, &fetch.State{})
	r.Seconds = pyfmt.RoundN(time.Since(t0).Seconds(), 1)
	r.Count = len(recs)
	if len(errs) > 0 {
		r.Errors = errs
	}

	switch {
	case len(errs) > 0:
		r.Status = "error"
	case len(recs) == 0:
		r.Status = "empty"
	default:
		r.Status = "ok"
	}
	return r
}

// marshalIndentNoEscape keeps &, < and > literal. python's json.dumps does not
// escape them and source error strings carry query-string urls.
func marshalIndentNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
