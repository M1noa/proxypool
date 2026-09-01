package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeEmpty(t *testing.T) {
	if got := string(Encode(nil)); got != "[]" {
		t.Errorf("Encode(nil) = %q, want %q", got, "[]")
	}
}

func TestEncodeFifteenKeysNoCheck(t *testing.T) {
	rt := 123
	r := &Record{
		IP: "1.2.3.4", IPVersion: "4", Port: 8080,
		Protocols: []string{"http"}, Country: "US", Anonymity: "elite",
		HTTPS: true, Sources: []string{"src1"},
		SourceMeta:     map[string]any{"b": "2", "a": "1"},
		ResponseTimeMS: &rt,
	}
	got := string(Encode([]*Record{r}))

	want := `[
  {
    "ip": "1.2.3.4",
    "ip_version": "4",
    "port": 8080,
    "protocols": [
      "http"
    ],
    "country": "US",
    "anonymity": "elite",
    "https": true,
    "sources": [
      "src1"
    ],
    "source_meta": {
      "a": "1",
      "b": "2"
    },
    "last_checked": null,
    "response_time_ms": 123,
    "response_time_raw_ms": null,
    "asn": null,
    "as_org": "",
    "ip_type": ""
  }
]`
	if got != want {
		t.Errorf("Encode mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(got), &arr); err != nil {
		t.Fatalf("Encode produced invalid json: %v", err)
	}
	if _, ok := arr[0]["reliability"]; ok {
		t.Error("check-less record must not have a reliability key at all")
	}
}

func TestEncodeTwentyOneKeysWithCheck(t *testing.T) {
	fs := "2026-01-01T00:00:00Z"
	r := &Record{
		IP: "1.2.3.4", Port: 80,
		Check: &CheckFields{
			Reliability: 0.9, Quality: 0.5,
			ChecksTotal: 10, ChecksOK: 9,
			FirstSeen: &fs, LastSeen: nil,
		},
	}
	var arr []map[string]any
	if err := json.Unmarshal(Encode([]*Record{r}), &arr); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	obj := arr[0]
	for _, k := range []string{"reliability", "quality", "checks_total", "checks_ok", "first_seen", "last_seen"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing key %q with Check set", k)
		}
	}
	if obj["last_seen"] != nil {
		t.Errorf("last_seen = %v, want null", obj["last_seen"])
	}
	if obj["reliability"] != 0.9 {
		t.Errorf("reliability = %v, want 0.9", obj["reliability"])
	}
}

func TestEncodeNonASCIIEscaped(t *testing.T) {
	orig := "Café 日本 \U00010348"
	r := &Record{IP: "1.2.3.4", AsOrg: orig}
	got := string(Encode([]*Record{r}))
	if strings.ContainsRune(got, 'é') || strings.ContainsRune(got, '日') {
		t.Errorf("non-ascii chars leaked unescaped into: %s", got)
	}
	esc := []string{"\\u00e9", "\\u65e5", "\\u672c"}
	for _, e := range esc {
		if !strings.Contains(got, e) {
			t.Errorf("missing escape %s in: %s", e, got)
		}
	}
	// U+10348 is above the BMP: must be a utf-16 surrogate pair.
	if !strings.Contains(got, "\\ud800\\udf48") {
		t.Errorf("missing surrogate pair for astral codepoint: %s", got)
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(got), &arr); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if arr[0]["as_org"] != orig {
		t.Errorf("round-trip mismatch: got %v", arr[0]["as_org"])
	}
}

func TestEncodeAngleBracketsAndAmpUnescaped(t *testing.T) {
	r := &Record{IP: "1.2.3.4", Country: "<a>&b</a>"}
	got := string(Encode([]*Record{r}))
	if !strings.Contains(got, `"<a>&b</a>"`) {
		t.Errorf("<>& should be left unescaped, got: %s", got)
	}
}

func TestEncodeJSONNumberPassthrough(t *testing.T) {
	r := &Record{
		IP: "1.2.3.4",
		SourceMeta: map[string]any{
			"score": json.Number("5"),
			"ratio": json.Number("5.0"),
		},
	}
	got := string(Encode([]*Record{r}))
	if !strings.Contains(got, `"score": 5,`) && !strings.Contains(got, "\"score\": 5\n") {
		t.Errorf("json.Number(5) should render bare as 5, got: %s", got)
	}
	if !strings.Contains(got, `"ratio": 5.0`) {
		t.Errorf("json.Number(5.0) should keep its original .0, got: %s", got)
	}
}

func TestEncodeEmptySliceAndMapInline(t *testing.T) {
	r := &Record{IP: "1.2.3.4", Protocols: []string{}, Sources: []string{}, SourceMeta: map[string]any{}}
	got := string(Encode([]*Record{r}))
	if !strings.Contains(got, `"protocols": [],`) {
		t.Errorf("empty protocols should render inline as [], got: %s", got)
	}
	if !strings.Contains(got, `"source_meta": {},`) {
		t.Errorf("empty source_meta should render inline as {}, got: %s", got)
	}
}

func TestEncodeNoTrailingNewline(t *testing.T) {
	got := Encode([]*Record{{IP: "1.2.3.4"}})
	if len(got) == 0 || got[len(got)-1] == '\n' {
		t.Errorf("Encode output must not end with a newline, got trailing byte %q", got[len(got)-1])
	}
}
