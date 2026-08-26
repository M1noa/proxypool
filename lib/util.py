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
    if "elite" in s or s in ("ha", "high"):
        return "elite"
    if "anon" in s and "not" not in s:
        return "anonymous"
    if "transparent" in s:
        return "transparent"
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


def get(url, timeout=30, headers=None):
    import requests

    h = {"user-agent": "proxypool/1.0"}
    if headers:
        h.update(headers)
    r = requests.get(url, timeout=timeout, headers=h)
    r.raise_for_status()
    return r.text
