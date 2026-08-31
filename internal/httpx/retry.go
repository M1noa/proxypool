package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// retry policy, verbatim from lib/util.request's defaults.
const (
	MaxAttempts = 6
	Backoff     = 2.0
	MaxWait     = 120 * time.Second
)

// statuses lib/util.request retries. anything else 4xx/5xx fails immediately,
// because requests' raise_for_status is outside the retry except clause.
var retryable = map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true}

// ErrBudget is python's TimeoutError("source budget exceeded"). the string is
// load-bearing: it reaches the readme error column via lib/parse's
// "%s: fetch failed: %v".
var ErrBudget = errors.New("source budget exceeded")

// StatusError is requests' HTTPError. the format is reproduced because
// fetch_all requeues a source by substring-matching "429" on the message.
type StatusError struct {
	Code   int
	Reason string
	URL    string
}

func (e *StatusError) Error() string {
	kind := "Client Error"
	if e.Code >= 500 {
		kind = "Server Error"
	}
	return fmt.Sprintf("%d %s: %s for url: %s", e.Code, kind, e.Reason, e.URL)
}

// Req is one call. Deadline is the source budget; zero means unbounded.
type Req struct {
	URL      string
	Method   string
	Body     any
	BodyType string
	Headers  map[string]string
	Deadline time.Time
}

// Get is the common case.
func (c *Client) Get(ctx context.Context, url string, deadline time.Time) (string, error) {
	return c.Do(ctx, Req{URL: url, Deadline: deadline})
}

// Do runs one http call with retry and backoff, returning the decoded body.
func (c *Client) Do(ctx context.Context, r Req) (string, error) {
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body []byte
	var contentType string
	if method == http.MethodPost {
		var err error
		if body, contentType, err = encodeBody(r.Body, r.BodyType); err != nil {
			return "", err
		}
	}

	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if !r.Deadline.IsZero() && time.Now().After(r.Deadline) {
			return "", ErrBudget
		}
		text, resp, err := c.once(ctx, method, r, body, contentType)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return "", err
			}
			if attempt == MaxAttempts {
				return "", err
			}
			if err := sleep(ctx, backoffWait(attempt), r.Deadline); err != nil {
				return "", err
			}
			continue
		}
		if retryable[resp.StatusCode] && attempt < MaxAttempts {
			if err := sleep(ctx, waitFor(resp, attempt), r.Deadline); err != nil {
				return "", err
			}
			continue
		}
		if resp.StatusCode >= 400 {
			return "", &StatusError{
				Code:   resp.StatusCode,
				Reason: reasonOf(resp),
				URL:    r.URL,
			}
		}
		return text, nil
	}
	return "", lastErr
}

// once performs a single attempt, always draining and closing the body so the
// connection returns to the pool.
func (c *Client) once(ctx context.Context, method string, r Req, body []byte,
	contentType string) (string, *http.Response, error) {

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL, rdr)
	if err != nil {
		return "", nil, err
	}
	req.Header = c.headers.Clone()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	return decodeBody(resp.Header.Get("Content-Type"), raw), resp, nil
}

// waitFor honors Retry-After when it is a number of seconds. python's
// float(ra) rejects the http-date form and falls through to backoff, and a
// numeric value is deliberately not clamped by MaxWait.
func waitFor(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.ParseFloat(strings.TrimSpace(ra), 64); err == nil {
			return time.Duration(secs * float64(time.Second))
		}
	}
	return backoffWait(attempt)
}

// backoffWait is min(2**attempt, MaxWait) with a 1-based attempt: 2s, 4s, 8s,
// 16s, 32s.
func backoffWait(attempt int) time.Duration {
	d := time.Duration(math.Pow(Backoff, float64(attempt)) * float64(time.Second))
	return min(d, MaxWait)
}

// sleep waits d, clamped to whatever is left of the source budget.
func sleep(ctx context.Context, d time.Duration, deadline time.Time) error {
	if !deadline.IsZero() {
		d = min(d, time.Until(deadline))
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func reasonOf(resp *http.Response) string {
	s := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
	if s == "" {
		s = http.StatusText(resp.StatusCode)
	}
	return s
}

// encodeBody mirrors requests' data= vs json= kwargs, including the `body or
// {}` fallback that turns an absent body into an empty one.
//
// requests would emit form fields in dict insertion order; url.Values sorts.
// only one source posts a body and its four fields are order-independent.
func encodeBody(body any, bodyType string) (buf []byte, contentType string, err error) {
	if bodyType == "form" {
		vals := url.Values{}
		for k, v := range asMap(body) {
			vals.Set(k, str(v))
		}
		return []byte(vals.Encode()), "application/x-www-form-urlencoded", nil
	}
	m := asMap(body)
	if m == nil {
		m = map[string]any{}
	}
	buf, err = json.Marshal(m)
	return buf, "application/json", err
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// str renders a form value the way python's str() would for the types json
// unmarshalling produces.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
