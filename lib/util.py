import json
import re
from pathlib import Path


def load_jsonc(path):
    text = Path(path).read_text()
    # strip // comments (not inside strings)
    out = []
    in_str = False
    esc = False
    i = 0
    while i < len(text):
        c = text[i]
        if in_str:
            out.append(c)
            if esc:
                esc = False
            elif c == "\\":
                esc = True
            elif c == '"':
                in_str = False
            i += 1
            continue
        if c == '"':
            in_str = True
            out.append(c)
        elif c == "/" and i + 1 < len(text) and text[i + 1] == "/":
            while i < len(text) and text[i] != "\n":
                i += 1
            continue
        else:
            out.append(c)
        i += 1
    return json.loads("".join(out))


def dig(obj, path):
    """dotted path lookup; empty path returns obj"""
    cur = obj
    for part in filter(None, path.split(".")):
        if not isinstance(cur, dict) or part not in cur:
            return None
        cur = cur[part]
    return cur


def normalize_anon(v):
    """map assorted anonymity values to transparent|anonymous|elite|''"""
    s = str(v or "").strip().lower()
    if "elite" in s or s in ("ha", "high", "高匿"):
        return "elite"
    if s in ("anon", "trans"):
        return {"anon": "anonymous", "trans": "transparent"}[s]
    if "anon" in s and "not" not in s:
        return "anonymous"
    if "transparent" in s or s in ("low", "透明", "notanonymous"):
        return "transparent"
    if s in ("average", "medium", "normal", "普匿"):
        return "anonymous"
    return ""


def to_bool(v):
    if isinstance(v, bool):
        return v
    s = str(v or "").strip().lower()
    if s in ("true", "yes", "1"):
        return True
    if s in ("false", "no", "0", "", "none"):
        return False
    return bool(v)


def parse_protocol(v):
    """split protocol-ish strings into a list of known protocols"""
    known = ("http", "https", "socks4", "socks5")
    parts = re.split(r"[/,|\s]+", str(v or "").strip().lower())
    return [p for p in parts if p in known]


BROWSER_UA = ("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
              "AppleWebKit/537.36 (KHTML, like Gecko) "
              "Chrome/126.0.0.0 Safari/537.36")


def browser_headers():
    """realistic browser header set for sources behind bot detection"""
    return {
        "user-agent": BROWSER_UA,
        "accept": ("text/html,application/xhtml+xml,application/xml;q=0.9,"
                   "image/avif,image/webp,*/*;q=0.8"),
        "accept-language": "en-US,en;q=0.9",
        "cache-control": "no-cache",
        "pragma": "no-cache",
        "upgrade-insecure-requests": "1",
    }


def make_session(src=None):
    """requests session for a source; antibot=True upgrades to browser headers"""
    import requests

    src = src or {}
    s = requests.Session()
    s.headers["user-agent"] = "proxypool/1.0"
    if src.get("antibot"):
        s.headers.update(browser_headers())
    s.headers.update(src.get("headers") or {})
    return s


def request(url, method="GET", body=None, body_type=None,
            timeout=30, session=None, headers=None,
            max_attempts=6, backoff=2.0, max_wait=120):
    """single http call with retry/backoff; honors Retry-After on 429/5xx,
    retries connection errors and timeouts; gives up after max_attempts.
    body_type 'form' posts form-encoded, default json"""
    import time as _time
    import requests as _requests

    sess = session or make_session()
    h = dict(sess.headers)
    if headers:
        h.update(headers)

    last_err = None
    for attempt in range(1, max_attempts + 1):
        try:
            if method.upper() == "POST":
                if body_type == "form":
                    r = sess.post(url, data=body or {}, timeout=timeout, headers=h)
                else:
                    r = sess.post(url, json=body or {}, timeout=timeout, headers=h)
            else:
                r = sess.get(url, timeout=timeout, headers=h)
            if r.status_code in (429, 500, 502, 503, 504) and attempt < max_attempts:
                ra = r.headers.get("retry-after")
                try:
                    wait = float(ra) if ra else min(backoff ** attempt, max_wait)
                except (TypeError, ValueError):
                    wait = min(backoff ** attempt, max_wait)
                _time.sleep(wait)
                continue
            r.raise_for_status()
            return r.text
        except (_requests.exceptions.ConnectionError,
                _requests.exceptions.Timeout) as e:
            last_err = e
            if attempt < max_attempts:
                _time.sleep(min(backoff ** attempt, max_wait))
                continue
            raise
    if last_err:
        raise last_err
    return r.text


def get(url, timeout=30, headers=None, session=None):
    return request(url, timeout=timeout, headers=headers, session=session)
