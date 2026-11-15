// package reputation tracks three public threat feeds in one compact lookup
// table: how many blacklists stamparm/ipsum reports an address on,
// FFRaud-com/ip-fraud-database's 0-100 score, confirmation count, category and
// infrastructure type, and Spamhaus's DROP and ASN-DROP lists of netblocks and
// operators they have tied to spam and cybercrime.
//
// none of these is a proxy source, so none of it is merged into the records
// themselves. a run loads the feeds once, attaches what they say about an
// address to that record, and scales the record's quality down by a bounded
// factor. a feed is someone else's opinion about an address, not a measurement
// of it, which is why the penalty tops out at maxPenalty instead of dropping
// the proxy: the record still answered our own probe, and that is the part we
// can see for ourselves.
//
// every file is re-downloaded each run and kept in the cache dir. a failed
// download falls back to the cached copy, so an upstream outage degrades to
// slightly stale data rather than no data, and only a run with nothing at all
// warns and carries on without the fields.
package reputation

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// the feeds. vars rather than consts only so tests can point them at an
// httptest server instead of the real upstream.
//
// EDROP is not listed separately: Spamhaus folded it into drop.txt, and the old
// edrop.txt url now serves only that note. ASN-DROP's plain-text variant is
// gone too, replaced by a newline-delimited json file.
var (
	ipsumURL   = "https://raw.githubusercontent.com/stamparm/ipsum/master/ipsum.txt"
	fraudURL   = "https://raw.githubusercontent.com/FFraud-com/ip-fraud-database/main/threat-ips/confirmed-abusive.csv"
	dropURL    = "https://www.spamhaus.org/drop/drop.txt"
	dropv6URL  = "https://www.spamhaus.org/drop/dropv6.txt"
	asnDropURL = "https://www.spamhaus.org/drop/asndrop.json"
)

const (
	downloadTimeout = 120 * time.Second
	userAgent       = "proxypool/1.0"

	// ipsum is ~137k lines, the fraud csv ~1.65m rows. neither is handed to a
	// scanner with default buffer limits.
	readBufSize = 1 << 16

	// listCap is the ipsum blacklist count that maxes the blacklist signal.
	// most of what ipsum lists is on exactly one list (104k of 139k rows), so
	// capping at 6 leaves only the ~9k addresses many feeds independently agree
	// on scoring full marks.
	listCap = 6

	// maxPenalty caps the aggregate feeds' weight on a record's quality. they
	// move the score, they do not override the probe.
	maxPenalty = 0.30

	// netPenalty is Spamhaus's weight, and it is meant to bite. ipsum and ffrraud
	// are vote counts; DROP is Spamhaus stating that the netblock was leased to
	// someone it has tied to spam or cybercrime, which is a different kind of
	// claim and earns a heavier one. the two penalties compose, so the worst case
	// lands near 0.385 rather than at either cap alone.
	netPenalty = 0.45

	// dropRisk and asnRisk are how much of netPenalty each list commands. DROP
	// names the netblock itself, so it stands at full weight; ASN-DROP names the
	// operator, which says less about one address inside a large asn.
	dropRisk = 1.0
	asnRisk  = 0.6

	// packed value layout inside Feed's maps. one uint32 per address instead of
	// a struct: 1.65m entries is the difference between ~30mb and ~150mb of heap
	// while the pipeline is already holding every record it fetched.
	scoreBits    = 7 // 0-6   fraud score, 0 when not in the feed
	reportsBits  = 6 // 7-12  confirmations, saturated at 63
	categoryBits = 4 // 13-16 category index
	typeBits     = 3 // 17-19 infrastructure type index
	listsBits    = 4 // 21-24 ipsum blacklists, 0 when not listed

	scoreShift    = 0
	reportsShift  = scoreShift + scoreBits
	categoryShift = reportsShift + reportsBits
	typeShift     = categoryShift + categoryBits
	listsShift    = typeShift + typeBits + 1 // bit 20 left spare

	scoreMask    = 1<<scoreBits - 1
	reportsMask  = 1<<reportsBits - 1
	categoryMask = 1<<categoryBits - 1
	typeMask     = 1<<typeBits - 1
	listsMask    = 1<<listsBits - 1
)

// categories and infraTypes are indexed by the packed value, so both are sized
// to their bit width rather than to the number of values the feed publishes
// today. anything the feed adds later decodes as "" instead of panicking.
var categories = [1 << categoryBits]string{
	"", "c2", "malware", "botnet", "brute_force",
	"web_attack", "scanner", "phishing", "spam", "abuse",
}

var infraTypes = [1 << typeBits]string{
	"", "proxy", "vpn", "tor", "datacenter", "mobile",
}

var categoryIndex = indexOf(categories[:])
var typeIndex = indexOf(infraTypes[:])

func indexOf(table []string) map[string]uint32 {
	m := make(map[string]uint32, len(table))
	for i, s := range table {
		if s != "" {
			m[s] = uint32(i)
		}
	}
	return m
}

// Entry is what the feeds say about one address. the zero Entry means none of
// them lists it.
type Entry struct {
	Blacklists    int    `json:"blacklists"`
	FraudScore    int    `json:"fraud_score"`
	FraudReports  int    `json:"fraud_reports"`
	FraudCategory string `json:"fraud_category"`
	FraudType     string `json:"fraud_type"`
	SpamhausDrop  bool   `json:"spamhaus_drop"`
	SpamhausASN   bool   `json:"spamhaus_asn_drop"`
}

// Risk is the 0..1 strength of the aggregate feeds' signals, combined so that
// being on many blacklists and being scored badly by ffrraud reinforce each
// other without either one alone reaching 1.
func (e Entry) Risk() float64 {
	if e.Blacklists == 0 && e.FraudScore == 0 {
		return 0
	}
	listRisk := math.Min(1, float64(e.Blacklists)/listCap)
	fraudRisk := math.Min(1, float64(e.FraudScore)/100)
	return 1 - (1-listRisk)*(1-fraudRisk)
}

// NetRisk is the 0..1 strength of the Spamhaus signals on their own, and is
// scored separately from Risk so that a netblock listing is not diluted by
// whatever the vote-count feeds happen to say.
func (e Entry) NetRisk() float64 {
	switch {
	case e.SpamhausDrop:
		return dropRisk
	case e.SpamhausASN:
		return asnRisk
	}
	return 0
}

// QualityFactor is the multiplier a record's quality is scaled by. exactly 1 for
// an address no feed lists; at worst (1-maxPenalty)*(1-netPenalty) for one that
// is on every aggregate list and inside a DROP netblock.
func (e Entry) QualityFactor() float64 {
	return (1 - maxPenalty*e.Risk()) * (1 - netPenalty*e.NetRisk())
}

// Feed is every dataset in one table, keyed by address.
//
// ipsum and ffrraud publish addresses, so they fold into the two maps. Spamhaus
// publishes netblocks and asns instead, which cannot be expanded to addresses,
// so DROP stays a range table searched at lookup time and ASN-DROP a set.
type Feed struct {
	v4 map[uint32]uint32
	v6 map[[16]byte]uint32

	drop4 []netRange
	drop6 []netip.Prefix
	asn   map[uint32]struct{}

	ipsumN int
	fraudN int
	dropN  int
	asnN   int
}

// netRange is one Spamhaus DROP netblock as an inclusive ipv4 interval. ranges
// are coalesced at load, so lookups are a single binary search.
type netRange struct{ lo, hi uint64 }

// Len reports how many rows each feed contributed: addresses for ipsum and
// ffrraud, netblocks for drop, asns for asn-drop.
func (f *Feed) Len() (ipsum, fraud, drop, asn int) {
	if f == nil {
		return 0, 0, 0, 0
	}
	return f.ipsumN, f.fraudN, f.dropN, f.asnN
}

// Entry looks an address up. asn may be 0 when the caller has not resolved one;
// it is what makes the ASN-DROP half of the answer possible. Entry is safe on a
// nil Feed, which is how a run that reached no feed keeps the call sites free of
// nil checks.
func (f *Feed) Entry(ip string, asn int) (Entry, bool) {
	if f == nil {
		return Entry{}, false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return Entry{}, false
	}
	v, listed := f.get(addr)
	e := decode(v)
	e.SpamhausDrop = f.inDrop(addr)
	if asn > 0 {
		_, e.SpamhausASN = f.asn[uint32(asn)]
	}
	if !listed && !e.SpamhausDrop && !e.SpamhausASN {
		return Entry{}, false
	}
	return e, true
}

// inDrop reports whether addr is inside a Spamhaus DROP netblock. the ipv4
// ranges are coalesced and sorted, so this is one binary search; the ipv6 list
// is a few dozen prefixes and is simply scanned.
func (f *Feed) inDrop(addr netip.Addr) bool {
	if k, ok := addr4(addr); ok {
		i := sort.Search(len(f.drop4), func(i int) bool { return f.drop4[i].hi >= uint64(k) })
		return i < len(f.drop4) && f.drop4[i].lo <= uint64(k)
	}
	for _, p := range f.drop6 {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (f *Feed) get(addr netip.Addr) (uint32, bool) {
	if k, ok := addr4(addr); ok {
		v, found := f.v4[k]
		return v, found
	}
	k, ok := addr16(addr)
	if !ok {
		return 0, false
	}
	v, found := f.v6[k]
	return v, found
}

func (f *Feed) put(addr netip.Addr, v uint32) bool {
	if k, ok := addr4(addr); ok {
		if f.v4 == nil {
			f.v4 = make(map[uint32]uint32, 1<<21)
		}
		f.v4[k] = v
		return true
	}
	k, ok := addr16(addr)
	if !ok {
		return false
	}
	if f.v6 == nil {
		f.v6 = make(map[[16]byte]uint32, 1<<12)
	}
	f.v6[k] = v
	return true
}

func addr4(addr netip.Addr) (uint32, bool) {
	addr = addr.Unmap()
	if !addr.Is4() {
		return 0, false
	}
	b := addr.As4()
	return binary.BigEndian.Uint32(b[:]), true
}

func addr16(addr netip.Addr) ([16]byte, bool) {
	if addr.Is4() || addr.Is4In6() {
		return [16]byte{}, false
	}
	return addr.As16(), true
}

func decode(v uint32) Entry {
	return Entry{
		Blacklists:    int((v >> listsShift) & listsMask),
		FraudScore:    int((v >> scoreShift) & scoreMask),
		FraudReports:  int((v >> reportsShift) & reportsMask),
		FraudCategory: categories[(v>>categoryShift)&categoryMask],
		FraudType:     infraTypes[(v>>typeShift)&typeMask],
	}
}

// Load downloads every feed, parses them, and returns the merged table. It only
// returns an error when nothing at all produced a row, which the pipeline treats
// as a warning: the proxies are still worth publishing without this.
func Load(ctx context.Context, cacheDir string) (*Feed, error) {
	f := &Feed{}
	var problems []string

	// each feed is independent: one dead upstream leaves the others' data
	// intact and only loses its own signal.
	load := func(name, url, file string, parse func(string) (int, error)) int {
		path, err := refresh(ctx, url, filepath.Join(cacheDir, file))
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			return 0
		}
		n, err := parse(path)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			return 0
		}
		return n
	}

	f.ipsumN = load("ipsum", ipsumURL, "ipsum.txt", f.parseIpsum)
	f.fraudN = load("ffraud", fraudURL, "ffraud-confirmed-abusive.csv", f.parseFraud)
	f.dropN = load("spamhaus drop", dropURL, "spamhaus-drop.txt", f.parseDrop)
	// the ipv6 half shares drop's budget: it is the same list, and a v6 file
	// that fails should not read as "no drop data" when the v4 one loaded.
	f.dropN += load("spamhaus drop v6", dropv6URL, "spamhaus-dropv6.txt", f.parseDrop6)
	f.asnN = load("spamhaus asn-drop", asnDropURL, "spamhaus-asndrop.json", f.parseASNDrop)

	if f.ipsumN == 0 && f.fraudN == 0 && f.dropN == 0 && f.asnN == 0 {
		return nil, fmt.Errorf("no threat feed loaded (%s)", strings.Join(problems, "; "))
	}
	return f, nil
}

// refresh downloads url to dest, falling back to whatever is already there.
// unlike geoip's monthly snapshots these are rebuilt every 30 minutes, so there
// is no point in a cache-hit shortcut: always try the upstream, keep the old
// file if that fails.
func refresh(ctx context.Context, url, dest string) (string, error) {
	werr := download(ctx, url, dest)
	if werr == nil {
		return dest, nil
	}
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	return "", werr
}

// download streams url to dest via a temp file and a rename, so a truncated
// transfer never replaces a good cache entry.
func download(ctx context.Context, url, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("user-agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d %s", url, resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// parseIpsum reads "ip<TAB>count" rows, ignoring the feed's leading comments.
func (f *Feed) parseIpsum(path string) (int, error) {
	return eachLine(path, func(line string) bool {
		if line == "" || line[0] == '#' {
			return false
		}
		ip, count, ok := strings.Cut(line, "\t")
		if !ok {
			if fields := strings.Fields(line); len(fields) >= 2 {
				ip, count = fields[0], fields[1]
			} else {
				return false
			}
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(ip))
		if err != nil {
			return false
		}
		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil || n <= 0 {
			return false
		}
		// 10 lists is the feed's maximum today; clamp rather than wrap the
		// 4-bit field if it ever grows.
		v, _ := f.get(addr)
		v |= uint32(min(n, listsMask)) << listsShift
		return f.put(addr, v)
	})
}

// parseFraud reads the confirmed-abusive csv: ip,score,confirmations,category,
// type. rows that are short, unparseable or out of range are skipped rather
// than failing the load.
func (f *Feed) parseFraud(path string) (int, error) {
	return eachLine(path, func(line string) bool {
		if line == "" || line[0] == '#' {
			return false
		}
		fields := strings.Split(line, ",")
		if len(fields) < 4 || fields[0] == "ip" {
			return false
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(fields[0]))
		if err != nil {
			return false
		}
		score, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || score <= 0 {
			return false
		}
		reports, _ := strconv.Atoi(strings.TrimSpace(fields[2]))

		v, _ := f.get(addr)
		v &^= scoreMask
		v |= uint32(min(score, scoreMask)) << scoreShift
		v &^= reportsMask << reportsShift
		v |= uint32(min(max(reports, 0), reportsMask)) << reportsShift
		if idx, ok := categoryIndex[strings.TrimSpace(fields[3])]; ok {
			v &^= categoryMask << categoryShift
			v |= idx << categoryShift
		}
		if len(fields) > 4 {
			if idx, ok := typeIndex[strings.TrimSpace(fields[4])]; ok {
				v &^= typeMask << typeShift
				v |= idx << typeShift
			}
		}
		return f.put(addr, v)
	})
}

// parseDrop reads Spamhaus DROP's "cidr ; SBLref" rows into coalesced ipv4
// ranges. the comment header splits to an empty cidr and is skipped.
func (f *Feed) parseDrop(path string) (int, error) {
	var ranges []netRange
	n, err := eachLine(path, func(line string) bool {
		pfx, ok := parseDropLine(line)
		if !ok || !pfx.Addr().Is4() {
			return false
		}
		b := pfx.Addr().As4()
		size := uint64(1) << (32 - pfx.Bits())
		lo := uint64(binary.BigEndian.Uint32(b[:])) &^ (size - 1)
		ranges = append(ranges, netRange{lo: lo, hi: lo + size - 1})
		return true
	})
	if err != nil {
		return 0, err
	}

	// DROP itself is already merged, but coalescing costs nothing and makes the
	// "no two ranges overlap" assumption the binary search relies on ours.
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].lo < ranges[j].lo })
	merged := ranges[:0]
	for _, r := range ranges {
		if n := len(merged); n > 0 && r.lo <= merged[n-1].hi+1 {
			merged[n-1].hi = max(merged[n-1].hi, r.hi)
			continue
		}
		merged = append(merged, r)
	}
	f.drop4 = merged
	return n, nil
}

// parseDrop6 is parseDrop for the ipv6 list, kept as prefixes because a /32
// netblock does not fit an interval.
func (f *Feed) parseDrop6(path string) (int, error) {
	var pfx []netip.Prefix
	n, err := eachLine(path, func(line string) bool {
		p, ok := parseDropLine(line)
		if !ok || p.Addr().Is4() {
			return false
		}
		pfx = append(pfx, p.Masked())
		return true
	})
	f.drop6 = pfx
	return n, err
}

// parseDropLine pulls the cidr off a "; SBLref"-suffixed DROP row.
func parseDropLine(line string) (netip.Prefix, bool) {
	cidr := strings.TrimSpace(strings.SplitN(line, ";", 2)[0])
	if cidr == "" {
		return netip.Prefix{}, false
	}
	p, err := netip.ParsePrefix(cidr)
	return p, err == nil
}

// parseASNDrop reads ASN-DROP, one json object per line. the file ends with a
// metadata object that carries no asn, so a row without a positive one is
// skipped along with anything unparseable.
func (f *Feed) parseASNDrop(path string) (int, error) {
	set := make(map[uint32]struct{}, 1<<10)
	var rec struct {
		ASN int `json:"asn"`
	}
	n, err := eachLine(path, func(line string) bool {
		if line == "" || line[0] == ';' {
			return false
		}
		rec.ASN = 0
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.ASN <= 0 {
			return false
		}
		set[uint32(rec.ASN)] = struct{}{}
		return true
	})
	f.asn = set
	return n, err
}

// eachLine streams path line by line, calling fn with the line minus its
// newline, and counts the lines fn accepted. a feed that starts serving an html
// error page with a 200 parses to zero rows, which Load reports instead of
// silently publishing an empty lookup table.
func eachLine(path string, fn func(string) bool) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	r := bufio.NewReaderSize(file, readBufSize)
	n := 0
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			if fn(strings.TrimRight(line, "\r\n")) {
				n++
			}
		}
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}
