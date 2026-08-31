// package geoip ports lib/geoip.py: db-ip's free country and asn mmdb
// snapshots, plus ipverse's asn -> category dataset, all cached to disk.
package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	countryBase = "https://download.db-ip.com/free/dbip-country-lite-%s.mmdb.gz"
	asnBase     = "https://download.db-ip.com/free/dbip-asn-lite-%s.mmdb.gz"

	downloadTimeout   = 180 * time.Second
	categoriesTimeout = 300 * time.Second

	userAgent = "proxypool/1.0"
)

// asnMetaURL is a var, not a const, only so tests can point it at an
// httptest server instead of the real upstream.
var asnMetaURL = "https://raw.githubusercontent.com/ipverse/as-metadata/master/as.json"

// DownloadMMDB is download_mmdb.
func DownloadMMDB(ctx context.Context, cacheDir string) (string, error) {
	return download(ctx, countryBase, "dbip-country-lite.mmdb", cacheDir)
}

// DownloadASNMMDB is download_asn_mmdb.
func DownloadASNMMDB(ctx context.Context, cacheDir string) (string, error) {
	return download(ctx, asnBase, "dbip-asn-lite.mmdb", cacheDir)
}

// download is _download: cache hit returns immediately, otherwise tries the
// current month then the previous one, since db-ip publishes a new snapshot
// monthly and the current month's file may not exist yet on the 1st.
func download(ctx context.Context, base, destName, cacheDir string) (string, error) {
	dest := filepath.Join(cacheDir, destName)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}

	now := time.Now()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	prev := firstOfMonth.AddDate(0, 0, -1)

	var lastErr error
	for _, d := range []time.Time{now, prev} {
		ym := fmt.Sprintf("%04d-%02d", d.Year(), d.Month())
		url := fmt.Sprintf(base, ym)
		raw, err := fetchGzip(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, raw, 0o644); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("could not download %s: %w", destName, lastErr)
}

// fetchGzip gets url and returns the decompressed body, but only trusts a
// 200 that actually starts with the gzip magic bytes — a wrong-month url can
// 200 with an html error page on some cdns, which python guards against the
// same way (r.content[:2] == b"\x1f\x8b").
func fetchGzip(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return nil, fmt.Errorf("unexpected response for %s: status %d", url, resp.StatusCode)
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}

// DownloadASNCategories is download_asn_categories: asn -> category from
// ipverse's cc0 as-metadata dataset. the cached as.json is ~69mb, so it is
// stream-decoded token by token instead of slurped into one parsed tree the
// way python's json.loads(dest.read_text()) does.
func DownloadASNCategories(ctx context.Context, cacheDir string) (map[int]string, error) {
	dest := filepath.Join(cacheDir, "ipverse-as-categories.json")
	if _, err := os.Stat(dest); err != nil {
		if err := downloadASJSON(ctx, dest); err != nil {
			return nil, err
		}
	}

	f, err := os.Open(dest)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	if _, err := dec.Token(); err != nil { // consume the opening '['
		return nil, err
	}
	categories := map[int]string{}
	for dec.More() {
		var entry struct {
			ASN      int `json:"asn"`
			Metadata struct {
				Category string `json:"category"`
			} `json:"metadata"`
		}
		if err := dec.Decode(&entry); err != nil {
			return nil, err
		}
		if entry.Metadata.Category != "" {
			categories[entry.ASN] = entry.Metadata.Category
		}
	}
	return categories, nil
}

// downloadASJSON streams the response straight to disk (via a temp file plus
// rename) rather than buffering the whole 69mb body in memory.
func downloadASJSON(ctx context.Context, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, categoriesTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asnMetaURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("user-agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: %d %s", asnMetaURL, resp.StatusCode, http.StatusText(resp.StatusCode))
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
