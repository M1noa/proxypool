package output

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/config"
)

func writeTempReadme(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const readmeFixture = `# proxypool

![total](https://img.shields.io/badge/total%20proxies-0-blue)
![avg](https://img.shields.io/badge/avg%20response-0ms-blue)
![last](https://img.shields.io/badge/last%20check-2020-01-01-green)

<!-- sources:start -->
<!-- sources:end -->

<!-- types:start -->
<!-- types:end -->

<!-- countries:start -->
<!-- countries:end -->

<!-- anon:start -->
<!-- anon:end -->

<!-- proto:start -->
<!-- proto:end -->

<!-- ports:start -->
<!-- ports:end -->
`

func TestUpdateReadmeMissingFileIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	if err := UpdateReadme(path, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("UpdateReadme on missing file returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("UpdateReadme must not create a README that didn't exist")
	}
}

func TestUpdateReadmeBadges(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	rt1, rt2 := 100, 300
	records := []*Record{
		{IP: "1.1.1.1", Port: 1, ResponseTimeMS: &rt1},
		{IP: "2.2.2.2", Port: 2, ResponseTimeMS: &rt2},
	}
	when := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	if err := UpdateReadme(path, records, nil, nil, when); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "total%20proxies-2-blue") {
		t.Errorf("total badge not updated: %s", s)
	}
	if !strings.Contains(s, "avg%20response-200ms-blue") {
		t.Errorf("avg badge not updated: %s", s)
	}
	if !strings.Contains(s, "last%20check-2026--08--31-green") {
		t.Errorf("last-check badge not updated: %s", s)
	}
}

func TestUpdateReadmeAvgIgnoresNilAndZeroResponseTimes(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	zero := 0
	rt := 50
	records := []*Record{
		{IP: "1.1.1.1", Port: 1, ResponseTimeMS: &zero},
		{IP: "2.2.2.2", Port: 2, ResponseTimeMS: nil},
		{IP: "3.3.3.3", Port: 3, ResponseTimeMS: &rt},
	}
	if err := UpdateReadme(path, records, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "avg%20response-50ms-blue") {
		t.Errorf("avg should only average non-nil, non-zero response times: %s", got)
	}
}

func TestUpdateReadmeTypesTable(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	records := []*Record{
		{IP: "1", Port: 1, IPType: "residential"},
		{IP: "2", Port: 2, IPType: "residential"},
		{IP: "3", Port: 3, IPType: "datacenter"},
		{IP: "4", Port: 4}, // empty -> "unknown"
	}
	if err := UpdateReadme(path, records, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "<td "+cellStyle+">residential</td><td "+cellStyle+">2</td>") {
		t.Errorf("types table missing residential=2 row: %s", s)
	}
	if !strings.Contains(s, "unknown") {
		t.Errorf("types table missing unknown fallback: %s", s)
	}
}

func TestUpdateReadmeCountriesOther(t *testing.T) {
	// insertion order A,B,C,D,E,F with counts 1,1,1,1,5,5, so frequency order
	// and insertion order disagree. top(4) is E,F,A,B; "other" must be the
	// two countries that leaves out, C+D=2. python summed the insertion-order
	// tail instead, which double-counted E,F and dropped C,D.
	path := writeTempReadme(t, readmeFixture)
	var records []*Record
	add := func(country string, n int) {
		for i := 0; i < n; i++ {
			records = append(records, &Record{IP: country, Port: len(records) + 1, Country: country})
		}
	}
	add("A", 1)
	add("B", 1)
	add("C", 1)
	add("D", 1)
	add("E", 5)
	add("F", 5)

	if err := UpdateReadme(path, records, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := extractMarkerBlock(t, string(got), "countries")

	// top(4) by frequency: E=5, F=5, A=1, B=1 (ties broken by insertion order)
	for _, want := range []string{"E</td><td " + cellStyle + ">5", "F</td><td " + cellStyle + ">5",
		"A</td><td " + cellStyle + ">1", "B</td><td " + cellStyle + ">1"} {
		if !strings.Contains(s, want) {
			t.Errorf("countries table missing expected top row %q in: %s", want, s)
		}
	}
	if !strings.Contains(s, "other</td><td "+cellStyle+">2") {
		t.Errorf("countries 'other' = want C+D = 2, got: %s", s)
	}
	if strings.Contains(s, ">C<") || strings.Contains(s, ">D<") {
		t.Errorf("C and D belong in 'other', not their own rows: %s", s)
	}
}

func extractMarkerBlock(t *testing.T, text, name string) string {
	t.Helper()
	start := "<!-- " + name + ":start -->"
	end := "<!-- " + name + ":end -->"
	si := strings.Index(text, start)
	if si == -1 {
		t.Fatalf("marker %q not found", name)
	}
	ei := strings.Index(text[si:], end)
	if ei == -1 {
		t.Fatalf("end marker %q not found", name)
	}
	return text[si : si+ei]
}

func TestUpdateReadmeAnonAndProtoTables(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	records := []*Record{
		{IP: "1", Port: 1, Anonymity: "elite", Protocols: []string{"http", "https"}},
		{IP: "2", Port: 2, Protocols: []string{"socks5"}}, // anonymity "" -> unknown
	}
	if err := UpdateReadme(path, records, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	anon := extractMarkerBlock(t, s, "anon")
	if !strings.Contains(anon, "elite") || !strings.Contains(anon, "unknown") {
		t.Errorf("anon table missing entries: %s", anon)
	}
	proto := extractMarkerBlock(t, s, "proto")
	for _, want := range []string{"http", "https", "socks5"} {
		if !strings.Contains(proto, want) {
			t.Errorf("proto table missing %q: %s", want, proto)
		}
	}
}

func TestUpdateReadmePortsTableTop5(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	var records []*Record
	ports := []int{80, 80, 80, 443, 443, 8080, 1080, 3128, 9999}
	for i, p := range ports {
		records = append(records, &Record{IP: strconv.Itoa(i), Port: p})
	}
	if err := UpdateReadme(path, records, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	ptable := extractMarkerBlock(t, string(got), "ports")
	if !strings.Contains(ptable, ">80<") || !strings.Contains(ptable, ">443<") {
		t.Errorf("ports table missing most common ports: %s", ptable)
	}
	// only top 5 distinct ports should appear as rows; there are 6 distinct
	// ports (80,443,8080,1080,3128,9999) so exactly one must be dropped.
	count := 0
	for _, p := range []string{"8080", "1080", "3128", "9999"} {
		if strings.Contains(ptable, ">"+p+"<") {
			count++
		}
	}
	if count != 3 {
		t.Errorf("expected exactly 3 of the 4 singleton ports to survive top-5 truncation, got %d in: %s", count, ptable)
	}
}

func TestSourceLinkNamesGithubRepoFallback(t *testing.T) {
	sources := []config.Source{
		{Name: "s1", URL: "https://raw.githubusercontent.com/owner/repo/main/list.txt"},
	}
	home, display, order := sourceLinkNames(sources)
	if home["s1"] != "https://github.com/owner/repo" {
		t.Errorf("home = %q, want github repo link", home["s1"])
	}
	if display["s1"] != "owner/repo" {
		t.Errorf("display = %q, want owner/repo", display["s1"])
	}
	if len(order) != 1 || order[0] != "s1" {
		t.Errorf("order = %v", order)
	}
}

func TestSourceLinkNamesDisambiguatesCollidingRepos(t *testing.T) {
	sources := []config.Source{
		{Name: "s1", URL: "https://raw.githubusercontent.com/owner/repo/main/http.txt"},
		{Name: "s2", URL: "https://raw.githubusercontent.com/owner/repo/main/socks5.txt"},
	}
	_, display, _ := sourceLinkNames(sources)
	if display["s1"] != "owner/repo (http)" {
		t.Errorf("display[s1] = %q, want disambiguated with url stem", display["s1"])
	}
	if display["s2"] != "owner/repo (socks5)" {
		t.Errorf("display[s2] = %q, want disambiguated with url stem", display["s2"])
	}
}

func TestSourceLinkNamesExplicitHomeUnrecognizedURLNoDisplayStaysEmpty(t *testing.T) {
	// genuine python quirk: label only falls back to host/name INSIDE the
	// `if not link:` branch. an explicit `home` (making link truthy) with a
	// non-repo url and no `display` leaves label permanently "".
	sources := []config.Source{
		{Name: "weird", Home: "https://example.com/page", URL: "https://example.com/list.txt"},
	}
	home, display, _ := sourceLinkNames(sources)
	if home["weird"] != "https://example.com/page" {
		t.Errorf("home = %q, want the explicit home", home["weird"])
	}
	if display["weird"] != "" {
		t.Errorf("display = %q, want empty string (the quirk this test pins)", display["weird"])
	}
}

func TestSourceLinkNamesNoHomeNoRepoFallsBackToHost(t *testing.T) {
	sources := []config.Source{
		{Name: "plain", URL: "https://www.example.com/list.txt"},
	}
	home, display, _ := sourceLinkNames(sources)
	if home["plain"] != "https://example.com" {
		t.Errorf("home = %q, want www-stripped host link", home["plain"])
	}
	if display["plain"] != "example.com" {
		t.Errorf("display = %q, want bare host", display["plain"])
	}
}

func TestSourceLinkNamesEmptyURLFallsBackToName(t *testing.T) {
	sources := []config.Source{{Name: "flowsrc"}}
	home, display, _ := sourceLinkNames(sources)
	if home["flowsrc"] != "" {
		t.Errorf("home = %q, want empty (no url, no host)", home["flowsrc"])
	}
	if display["flowsrc"] != "flowsrc" {
		t.Errorf("display = %q, want source name fallback", display["flowsrc"])
	}
}

func TestUpdateReadmeSourcesTableSkipsCarriedRecords(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	rt := 100
	records := []*Record{
		{IP: "1", Port: 1, Sources: []string{"src1"}, ResponseTimeMS: &rt, Country: "US"},
		{IP: "2", Port: 2, Sources: []string{"src1"}, ResponseTimeMS: &rt, Country: "US", Carried: true},
	}
	fetched := map[string]int{"src1": 2}
	sources := []config.Source{{Name: "src1"}}
	if err := UpdateReadme(path, records, fetched, sources, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	stable := extractMarkerBlock(t, string(got), "sources")
	// alive should count only the 1 non-carried record, not both.
	if !strings.Contains(stable, ">1<") {
		t.Errorf("sources table alive count should exclude carried records: %s", stable)
	}
}

func TestUpdateReadmeSourcesTableBoldsTopRowAndSortsDescending(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	rtGood := 100
	records := []*Record{
		{IP: "1", Port: 1, Sources: []string{"good"}, ResponseTimeMS: &rtGood},
		{IP: "2", Port: 2, Sources: []string{"good"}, ResponseTimeMS: &rtGood},
		{IP: "3", Port: 3, Sources: []string{"bad"}},
	}
	fetched := map[string]int{"good": 2, "bad": 10}
	sources := []config.Source{{Name: "good"}, {Name: "bad"}}
	if err := UpdateReadme(path, records, fetched, sources, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	stable := extractMarkerBlock(t, string(got), "sources")
	goodIdx := strings.Index(stable, "good")
	badIdx := strings.Index(stable, "bad")
	if goodIdx == -1 || badIdx == -1 || goodIdx > badIdx {
		t.Errorf("expected 'good' (100%% success) to rank above 'bad' (10%% success): %s", stable)
	}
	if !strings.Contains(stable, "<b><a") && !strings.Contains(stable, "<b>good") {
		t.Errorf("top row must be bolded: %s", stable)
	}
}

func TestUpdateReadmeLayoutOrdersSourcesFirstThenFlexRow(t *testing.T) {
	path := writeTempReadme(t, readmeFixture)
	if err := UpdateReadme(path, nil, map[string]int{}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	sourcesIdx := strings.Index(s, "<!-- sources:start -->")
	flexIdx := strings.Index(s, `<div style="display:flex`)
	typesIdx := strings.Index(s, "<!-- types:start -->")
	if sourcesIdx == -1 || flexIdx == -1 || typesIdx == -1 {
		t.Fatalf("missing expected markers in: %s", s)
	}
	if !(sourcesIdx < flexIdx && flexIdx < typesIdx) {
		t.Errorf("expected order sources < flex-div < types, got sources=%d flex=%d types=%d", sourcesIdx, flexIdx, typesIdx)
	}
}
