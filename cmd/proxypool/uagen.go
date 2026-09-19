package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/M1noa/proxypool/internal/uagen"
)

// runUagen regenerates output/useragents.json from live version inputs.
func runUagen(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("uagen", flag.ExitOnError)
	out := fs.String("out", "", "output directory (default ./output)")
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	dir := *out
	if dir == "" {
		dir = filepath.Join(*root, "output")
	}
	t0 := time.Now()
	recs, warnings := uagen.Generate(ctx, time.Now())
	for _, w := range warnings {
		fmt.Println("WARN:", w)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := uagen.Write(filepath.Join(dir, "useragents.json"), recs); err != nil {
		return err
	}
	byBrowser := map[string]int{}
	for _, r := range recs {
		byBrowser[r.Browser]++
	}
	fmt.Printf("wrote %d user agents in %.1fs %v\n", len(recs),
		time.Since(t0).Seconds(), byBrowser)
	return nil
}
