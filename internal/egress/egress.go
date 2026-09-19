// package egress is the fetch layer's own proxy pool: the fastest
// http-capable proxies from a previous run's proxies.json, dealt out
// round-robin as egress for source fetching. attempt 1 of every fetch goes
// direct; only retries go through here, so a healthy source never pays proxy
// latency and a rate limiting one gets a fresh ip per attempt.
package egress

import (
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
)

// MaxPool caps the pool: enough distinct ips to ride out a 429 streak, few
// enough that every member is a fast one.
const MaxPool = 32

// entry is the subset of a proxies.json record the pool reads.
type entry struct {
	IP        string   `json:"ip"`
	Port      int      `json:"port"`
	Protocols []string `json:"protocols"`
	RT        *int     `json:"response_time_ms"`
}

// Pool deals proxy urls out round-robin. nil *Pool is valid and means
// direct-only; Next on it returns nil.
type Pool struct {
	urls []*url.URL
	next atomic.Uint64
}

// Load reads path (a proxies.json) and keeps the fastest http-capable
// entries. missing file or parse failure returns nil, not an error: egress
// is opportunistic, the fetch works direct without it.
func Load(path string) *Pool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries []entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	var fast []entry
	for _, e := range entries {
		if e.RT == nil || !slices.Contains(e.Protocols, "http") {
			continue
		}
		fast = append(fast, e)
	}
	slices.SortFunc(fast, func(a, b entry) int { return *a.RT - *b.RT })
	if len(fast) > MaxPool {
		fast = fast[:MaxPool]
	}
	p := &Pool{}
	for _, e := range fast {
		u, err := url.Parse("http://" + e.IP + ":" + strconv.Itoa(e.Port))
		if err != nil {
			continue
		}
		p.urls = append(p.urls, u)
	}
	if len(p.urls) == 0 {
		return nil
	}
	return p
}

// Next returns the next egress proxy url, rotating across the pool. nil when
// the pool is nil or empty.
func (p *Pool) Next() *url.URL {
	if p == nil || len(p.urls) == 0 {
		return nil
	}
	return p.urls[(p.next.Add(1)-1)%uint64(len(p.urls))]
}

// Len reports the pool size. nil pool has none.
func (p *Pool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.urls)
}
