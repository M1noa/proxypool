import re

from .util import dig, get, normalize_anon, parse_protocol, to_bool


def _norm_record(raw, src, default_proto=None):
    """raw: dict of extracted field values -> normalized record"""
    ip = str(raw.get("ip") or "").strip()
    try:
        port = int(str(raw.get("port") or "").strip())
    except (ValueError, TypeError):
        return None
    if not ip or not (1 <= port <= 65535):
        return None

    rec = {
        "ip": ip,
        "port": port,
        "protocols": [],
        "country": "",
        "country_name": "",
        "anonymity": "",
        "https": False,
        "response_time": None,
        "sources": [src["name"]],
        "source_meta": {},
    }

    # protocols
    protos = []
    if raw.get("protocols") and isinstance(raw["protocols"], list):
        for p in raw["protocols"]:
            protos.extend(parse_protocol(p))
    elif raw.get("protocol"):
        protos.extend(parse_protocol(raw["protocol"]))
    if not protos and default_proto:
        protos = [default_proto]
    rec["protocols"] = sorted(set(protos))

    # country
    code = str(raw.get("country") or "").strip()
    name = str(raw.get("country_name") or "").strip()
    if len(code) == 2 and code.isalpha():
        rec["country"] = code.upper()
    elif code:
        # a full name landed in the country slot
        name = name or code
    rec["country_name"] = name.strip()

    # anonymity / https / response time
    rec["anonymity"] = normalize_anon(raw.get("anonymity"))
    rec["https"] = to_bool(raw.get("https"))
    rt = raw.get("response_time")
    if isinstance(rt, (int, float)) and rt > 0:
        rec["response_time"] = float(rt)

    # extra metadata, nothing dropped
    meta = {k[len("meta_"):]: v for k, v in raw.items() if k.startswith("meta_") and v not in (None, "")}
    rec["source_meta"] = meta
    return rec


def _extract_json(items, ex):
    """yield raw dicts from json items (objects or strings)"""
    regex = ex.get("regex")
    for it in items:
        if isinstance(it, str):
            if not regex:
                continue
            m = re.match(regex, it)
            if not m:
                continue
            yield m.groupdict()
            continue
        out = {}
        for field in ("ip", "port", "protocol", "protocols", "country",
                      "country_name", "anonymity", "https", "response_time"):
            path = ex.get(field)
            if path:
                out[field] = dig(it, path)
        for mk, mv in (ex.get("source_meta") or {}).items():
            out[f"meta_{mk}"] = dig(it, mv)
        yield out


def _cell_text(row, sel):
    import re as _re
    from bs4 import BeautifulSoup

    spec = sel
    attr = None
    decode = None
    regex = None
    if isinstance(sel, dict):
        spec = sel.get("selector")
        attr = sel.get("attr")
        decode = sel.get("decode")
        regex = sel.get("regex")
    el = row.select_one(spec) if spec else row
    if el is None:
        return ""
    if regex:
        # prefer plain text (href/class noise), fall back to full markup
        hay = el.get_text(" ", strip=True)
        matches = [m.group(1) for m in _re.finditer(regex, hay)]
        if not matches:
            matches = [m.group(1) for m in _re.finditer(regex, str(el))]
        return "/".join(matches)
    if attr:
        val = el.get(attr, "") or ""
    else:
        val = el.get_text(" ", strip=True)
    if decode == "base64":
        import base64

        try:
            val = base64.b64decode(str(val)).decode()
        except Exception:
            return ""
    return str(val).strip()


def _extract_html(html_text, ex):
    from bs4 import BeautifulSoup

    soup = BeautifulSoup(html_text, "html.parser")
    fields = {}
    for f in ("ip", "port", "protocol", "country", "anonymity", "https", "response_time"):
        sel = ex.get(f)
        if sel:
            fields[f] = sel
    for row in soup.select(ex.get("row_selector", "tr")):
        out = {}
        for f, sel in fields.items():
            out[f] = _cell_text(row, sel)
        for mk, sel in (ex.get("source_meta") or {}).items():
            out[f"meta_{mk}"] = _cell_text(row, sel)
        yield out


def parse_content(src, content):
    """parse fetched content per source config -> list of normalized records"""
    fmt = src.get("format", "text")
    ex = src.get("extract", {})
    proto = src.get("protocol")
    recs = []

    if fmt == "json":
        import json as _json

        doc = _json.loads(content)
        items = dig(doc, ex.get("root", "")) if ex.get("root") else doc
        if items is None:
            items = doc
        if not isinstance(items, list):
            items = [items]
        raws = _extract_json(items, ex)
        for r in raws:
            rec = _norm_record(r, src, proto)
            if rec:
                recs.append(rec)

    elif fmt == "text":
        regex = re.compile(ex["regex"])
        for line in content.splitlines():
            m = regex.match(line.strip())
            if not m:
                continue
            g = m.groupdict()
            # inline prefix wins over file default
            r = {
                "ip": g.get("ip"),
                "port": g.get("port"),
                "country": g.get("country"),
                "protocol": g.get("proto"),
                "meta_country": g.get("country"),
            }
            rec = _norm_record(r, src, proto)
            if rec:
                # hideip-style trailing country is a NAME not ISO code
                if g.get("country") and not rec["country"]:
                    rec["country"], rec["country_name"] = "", g["country"]
                    rec["source_meta"].pop("country", None)
                recs.append(rec)

    elif fmt == "html":
        for r in _extract_html(content, ex):
            rec = _norm_record(r, src, proto)
            if rec:
                recs.append(rec)

    return recs


def fetch_source(src, timeout=30, max_pages_default=20):
    """fetch all pages of a source -> (records, errors[])"""
    recs = []
    errors = []
    pag = src.get("pagination")

    def fetch_once(url):
        return get(url, timeout=timeout, headers=src.get("headers"))

    urls = [src["url"]]
    if src.get("fallback_url"):
        urls.append(src["fallback_url"])

    if not pag:
        content = None
        for i, url in enumerate(urls):
            try:
                content = fetch_once(url)
                break
            except Exception as e:
                if i == len(urls) - 1:
                    errors.append(f"{src['name']}: fetch failed: {e}")
        if content is not None:
            try:
                recs = parse_content(src, content)
            except Exception as e:
                errors.append(f"{src['name']}: parse failed: {e}")
        return recs, errors

    ptype = pag.get("type", "page")
    page = pag.get("start", 1)
    step = pag.get("step", 1)
    max_pages = pag.get("max_pages", max_pages_default)
    delay = pag.get("delay_ms", 0) / 1000.0

    for n in range(max_pages):
        url = src["url"].replace("{page}", str(page)).replace("{offset}", str(page))
        content = None
        for attempt in range(3):
            if delay:
                import time

                time.sleep(delay)
            try:
                content = fetch_once(url)
                break
            except Exception as e:
                msg = str(e)
                retryable = "429" in msg or "500" in msg or "502" in msg or "503" in msg
                if attempt < 2 and retryable:
                    import time

                    time.sleep(2 ** (attempt + 1))
                else:
                    errors.append(f"{src['name']} page={page}: fetch failed: {e}")
                    break
        if content is None:
            break
        try:
            batch = parse_content(src, content)
        except Exception as e:
            errors.append(f"{src['name']} page={page}: parse failed: {e}")
            break
        if not batch:
            break
        recs.extend(batch)
        page += step

    return recs, errors
