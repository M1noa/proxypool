// package reputation tracks two public threat feeds in one compact lookup
// table: how many blacklists stamparm/ipsum reports an address on, and
// FFRaud-com/ip-fraud-database's 0-100 score, confirmation count, category and
// infrastructure type.
//
// neither feed is a proxy source, so none of this is merged into the records
// themselves. a run loads the feeds once, attaches what they say about an
// address to that record, and scales the record's quality down by a bounded
// factor. a feed is someone else's opinion about an address, not a measurement
// of it, which is why the penalty tops out at maxPenalty instead of dropping
// the proxy: the record still answered our own probe, and that is the part we
// can see for ourselves.
//
// both files are re-downloaded every run and kept in the cache dir. a failed
// download falls back to the cached copy, so an upstream outage degrades to
// slightly stale data rather than no data, and only a run with neither warns
// and carries on without the fields.
package reputation

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// the two feeds. vars rather than consts only so tests can point them at an
// httptest server instead of the real upstream.
var (
	ipsumURL = "https://raw.githubusercontent.com/stamparm/ipsum/master/ipsum.txt"
	fraudURL = "https://raw.githubusercontent.com/FFraud-com/ip-fraud-database/main/threat-ips/confirmed-abusive.csv"
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

	// maxPenalty caps how much the feeds may take off a record's quality. they
	// move the score, they do not override the probe.
	maxPenalty = 0.30

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

// Entry is what the two feeds say about one address. the zero Entry means
// neither of them lists it.
type Entry struct {
	Blacklists    int    `json:"blacklists"`
	FraudScore    int    `json:"fraud_score"`
	FraudReports  int    `json:"fraud_reports"`
	FraudCategory string `json:"fraud_category"`
	FraudType     string `json:"fraud_type"`
}

// Risk is the 0..1 strength of the hostile signals, combined so that being on
// many blacklists and being scored badly by ffrraud reinforce each other
// without either one alone reaching 1.
func (e Entry) Risk() float64 {
	if e.Blacklists == 0 && e.FraudScore == 0 {
		return 0
	}
	listRisk := math.Min(1, float64(e.Blacklists)/listCap)
	fraudRisk := math.Min(1, float64(e.FraudScore)/100)
	return 1 - (1-listRisk)*(1-fraudRisk)
}

// QualityFactor is the multiplier a record's quality is scaled by: exactly 1
// for an address the feeds do not list, down to 1-maxPenalty for the worst
// case (6+ blacklists and a 100 fraud score).
func (e Entry) QualityFactor() float64 {
	return 1 - maxPenalty*e.Risk()
}

// Feed is both datasets in one table, keyed by address.
type Feed struct {
	v4 map[uint32]uint32
	v6 map[[16]byte]uint32

	ipsumN int
	fraudN int
}

// Len reports how many addresses each feed contributed.
func (f *Feed) Len() (ipsum, fraud int) {
	if f == nil {
		return 0, 0
	}
	return f.ipsumN, f.fraudN
}

// Entry looks an address up. it is safe on a nil Feed, which is how a run that
// could not reach either feed keeps the call sites free of nil checks.
func (f *Feed) Entry(ip string) (Entry, bool) {
	if f == nil || len(f.v4) == 0 && len(f.v6) == 0 {
		return Entry{}, false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return Entry{}, false
	}
	v, ok := f.get(addr)
	if !ok {
		return Entry{}, false
	}
	return decode(v), true
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

// Load downloads both feeds, parses them, and returns the merged table. It only
// returns an error when neither feed produced a single row, which the pipeline
// treats as a warning: the proxies are still worth publishing without this.
func Load(ctx context.Context, cacheDir string) (*Feed, error) {
	f := &Feed{}
	var problems []string

	ipsumPath, err := refresh(ctx, ipsumURL, filepath.Join(cacheDir, "ipsum.txt"))
	if err != nil {
		problems = append(problems, "ipsum: "+err.Error())
	} else if n, err := f.parseIpsum(ipsumPath); err != nil {
		problems = append(problems, "ipsum: "+err.Error())
	} else {
		f.ipsumN = n
	}

	fraudPath, err := refresh(ctx, fraudURL, filepath.Join(cacheDir, "ffraud-confirmed-abusive.csv"))
	if err != nil {
		problems = append(problems, "ffraud: "+err.Error())
	} else if n, err := f.parseFraud(fraudPath); err != nil {
		problems = append(problems, "ffraud: "+err.Error())
	} else {
		f.fraudN = n
	}

	if f.ipsumN == 0 && f.fraudN == 0 {
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
