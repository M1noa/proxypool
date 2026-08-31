// command proxypool fetches proxies from every source in sources.jsonc, checks
// which ones answer, scores them against history, and writes output/proxies.json
// plus the README tables. with no subcommand it runs that whole pipeline; the
// subcommands are the debugging tools that used to live in tools/.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/M1noa/proxypool/internal/geoip"
	"github.com/M1noa/proxypool/internal/pipeline"
)

func main() {
	sub, argv := "", os.Args[1:]
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		sub, argv = argv[0], argv[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch sub {
	case "":
		err = runPipeline(ctx, argv)
	case "probe":
		err = runProbe(ctx, argv)
	case "test-source":
		err = runTestSource(ctx, argv)
	case "geoip":
		err = runGeoIP(ctx, argv)
	case "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", sub)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  proxypool [flags]                              run the full pipeline
  proxypool probe [-workers N] [-json]           per-source reachability report
  proxypool test-source <name> [-show N] [-list] fetch one source, print samples
  proxypool geoip [-cache DIR]                   warm the mmdb and asn caches

run "proxypool -h" for the pipeline flags.
`)
}

func runPipeline(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("proxypool", flag.ExitOnError)
	var o pipeline.Options

	fs.BoolVar(&o.SkipFetch, "skip-fetch", false, "reuse the previous proxies.json instead of fetching")
	fs.BoolVar(&o.SkipCheck, "skip-check", false, "fetch and merge only, no probing")
	fs.BoolVar(&o.SkipSpeedtest, "skip-speedtest", false, "skip the bandwidth measurement")
	fs.BoolVar(&o.SkipGeoIP, "skip-geoip", false, "no country lookup")
	fs.BoolVar(&o.SkipASN, "skip-asn", false, "no asn / as_org / ip_type lookup")
	fs.BoolVar(&o.SkipHistory, "skip-history", false, "no duckdb read/write, no scoring or skip-selection")
	fs.BoolVar(&o.SkipReadme, "skip-readme", false, "leave README.md alone")

	sources := fs.String("sources", "", "only these source names (comma separated)")
	exclude := fs.String("exclude", "", "drop these source names (comma separated)")
	formats := fs.String("format", "", "only sources with these formats (comma separated)")

	fs.IntVar(&o.Limit, "limit", 0, "cap records fed to the checker")
	fs.IntVar(&o.Concurrency, "concurrency", 0, "override derived checker concurrency")
	fs.DurationVar(&o.Timeout, "timeout", 0, "per-probe timeout (0 uses 5s)")
	fs.DurationVar(&o.Budget, "budget", 0, "wall-clock budget (0 uses 1h57m)")
	fs.StringVar(&o.Out, "out", "", "output directory (default ./output)")
	fs.BoolVar(&o.DryRun, "dry-run", false, "compute everything, write nothing")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	o.Only, o.Exclude, o.Formats = splitList(*sources), splitList(*exclude), splitList(*formats)
	if !*quiet {
		o.Logf = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	}
	return pipeline.Run(ctx, o)
}

func runGeoIP(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("geoip", flag.ExitOnError)
	cache := fs.String("cache", ".cache", "cache directory")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	t0 := time.Now()
	country, err := geoip.DownloadMMDB(ctx, *cache)
	if err != nil {
		return err
	}
	fmt.Println("country mmdb:", country)

	categories, err := geoip.DownloadASNCategories(ctx, *cache)
	if err != nil {
		return err
	}
	fmt.Printf("ipverse asn categories: %d classified\n", len(categories))

	asn, err := geoip.DownloadASNMMDB(ctx, *cache)
	if err != nil {
		return err
	}
	fmt.Println("asn mmdb:", asn)
	fmt.Printf("warmed in %.1fs\n", time.Since(t0).Seconds())
	return nil
}

// splitList parses a comma separated flag, dropping empty entries so a trailing
// comma or a bare "" does not filter everything out.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
