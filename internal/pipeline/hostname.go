package pipeline

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/M1noa/proxypool/internal/output"
)

// resolveHostnames fills in each record's PTR name. this is the one enrichment
// that is a request per address rather than a local file, so it runs only over
// the set about to be published, with a small worker pool and a hard deadline.
// a zone that does not answer in time costs that record its hostname and
// nothing else.
func resolveHostnames(ctx context.Context, recs []*output.Record, logf func(string, ...any)) {
	const (
		workers = 32
		perIP   = 3 * time.Second
		budget  = 120 * time.Second
	)
	if len(recs) == 0 {
		return
	}
	t0 := time.Now()
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var (
		idx      atomic.Int64
		resolved atomic.Int64
		kept     atomic.Int64
		wg       sync.WaitGroup
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(idx.Add(1) - 1)
				if i >= len(recs) || ctx.Err() != nil {
					return
				}
				// carried records arrive with the name the last run found. PTR
				// is stable enough that re-asking every hour for 2.8k addresses
				// buys nothing and costs the resolvers real traffic.
				if recs[i].Hostname != "" {
					kept.Add(1)
					continue
				}
				names := lookupPTR(ctx, recs[i].IP, perIP)
				if len(names) == 0 {
					continue
				}
				recs[i].Hostname = names[0]
				resolved.Add(1)
			}
		}()
	}
	wg.Wait()
	logf("hostnames: %d resolved, %d cached, %d/%d total in %.1fs",
		resolved.Load(), kept.Load(), resolved.Load()+kept.Load(), len(recs), time.Since(t0).Seconds())
}

// lookupPTR asks the resolver for one address, stripping the FQDN dot and
// preferring the first name, which is what dig prints first.
func lookupPTR(ctx context.Context, ip string, timeout time.Duration) []string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil {
		return nil
	}
	out := names[:0]
	for _, n := range names {
		if n = strings.TrimSuffix(n, "."); n != "" {
			out = append(out, n)
		}
	}
	return out
}
