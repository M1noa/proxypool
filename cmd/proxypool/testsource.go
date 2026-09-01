package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/fetch"
	"github.com/M1noa/proxypool/internal/flows"
)

// sampleRecord prints one fetched record in _norm_record's key order, which is
// what test_source.py dumps.
type sampleRecord struct {
	IP           string         `json:"ip"`
	IPVersion    string         `json:"ip_version"`
	Port         int            `json:"port"`
	Protocols    []string       `json:"protocols"`
	Country      string         `json:"country"`
	CountryName  string         `json:"country_name"`
	Anonymity    string         `json:"anonymity"`
	HTTPS        bool           `json:"https"`
	ResponseTime *float64       `json:"response_time"`
	Sources      []string       `json:"sources"`
	SourceMeta   map[string]any `json:"source_meta"`
	Provided     []string       `json:"_provided"`
}

// runTestSource is tools/test_source.py: fetch one source by name substring and
// print a sample, for checking a new sources.jsonc entry before a full run.
func runTestSource(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("test-source", flag.ExitOnError)
	maxPages := fs.Int("max-pages", 0, "cap pages (0 = source default)")
	show := fs.Int("show", 10, "sample records to print")
	list := fs.Bool("list", false, "list sources and exit")
	root := fs.String("root", ".", "repository root")
	// flag stops parsing at the first positional, so a leading source name would
	// swallow every flag after it. move it to the back.
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		argv = append(argv[1:], argv[0])
	}
	if err := fs.Parse(argv); err != nil {
		return err
	}

	sources, err := config.Load(filepath.Join(*root, "sources.jsonc"))
	if err != nil {
		return err
	}

	if *list {
		for _, s := range sources {
			antibot := "       "
			if s.Antibot {
				antibot = "antibot "
			}
			flow := ""
			if s.Flow != "" {
				flow = "flow:" + s.Flow
			}
			fmt.Printf("%-24s %-6s %s%s\n", s.Name, s.Format, antibot, flow)
		}
		return nil
	}

	name := strings.ToLower(fs.Arg(0))
	if name == "" {
		return fmt.Errorf("provide a source name/substring or -list")
	}
	var match []*config.Source
	for i := range sources {
		s := &sources[i]
		if strings.Contains(strings.ToLower(s.Name), name) || strings.Contains(strings.ToLower(s.Display), name) {
			match = append(match, s)
		}
	}
	if len(match) == 0 {
		fmt.Printf("no source matches %q\n", fs.Arg(0))
		return nil
	}

	f := &fetch.Fetcher{Flows: flows.Table}
	for _, src := range match {
		if *maxPages > 0 {
			capPages(src, *maxPages)
		}
		fmt.Printf("\n=== %s (%s) ===\n", src.Name, src.Display)
		recs, errs := f.Source(ctx, src, &fetch.State{})
		fmt.Printf("fetched %d raw records\n", len(recs))
		for i, r := range recs {
			if i >= *show {
				break
			}
			line, err := marshalNoEscape(sample(r))
			if err != nil {
				return err
			}
			fmt.Println(string(line))
		}
		for _, e := range errs {
			fmt.Println("ERR:", e)
		}
	}
	return nil
}

// capPages overrides the source's own max_pages, synthesizing pagination for a
// source that has none so -max-pages can force a single-page fetch either way.
func capPages(src *config.Source, n int) {
	if src.Pagination == nil {
		one := 1
		src.Pagination = &config.Pagination{Start: &one, Step: &one, Type: "page", Stop: "empty"}
	} else {
		p := *src.Pagination
		src.Pagination = &p
	}
	src.Pagination.MaxPages = &n
}

func sample(r *extract.Record) sampleRecord {
	provided := make([]string, 0, len(r.Provided))
	for f := range r.Provided {
		provided = append(provided, f)
	}
	sort.Strings(provided)
	return sampleRecord{
		IP:           r.IP,
		IPVersion:    r.IPVersion,
		Port:         r.Port,
		Protocols:    r.Protocols,
		Country:      r.Country,
		CountryName:  r.CountryName,
		Anonymity:    r.Anonymity,
		HTTPS:        r.HTTPS,
		ResponseTime: r.ResponseTime,
		Sources:      r.Sources,
		SourceMeta:   r.SourceMeta,
		Provided:     provided,
	}
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
