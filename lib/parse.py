import ipaddress
import json as _json
import math
import re

from .util import dig, get, make_session, normalize_anon, parse_protocol, request, to_bool


def ip_version_of(ip):
    """return 'ipv4' | 'ipv6' | 'domain' for a host string"""
    try:
        return "ipv6" if ipaddress.ip_address(ip).version == 6 else "ipv4"
    except ValueError:
        return "domain"


def _is_bogus_ip(ip):
    """reject loopback / private / link-local / multicast / reserved and 0/255 sentinels"""
    try:
        a = ipaddress.ip_address(ip)
    except ValueError:
        return False
    return (a.is_loopback or a.is_private or a.is_link_local
            or a.is_multicast or a.is_reserved or a.is_unspecified
            or str(a) == "255.255.255.255")


def _norm_record(raw, src, default_proto=None):
    """raw: dict of extracted field values -> normalized record"""
    ip = str(raw.get("ip") or "").strip()
    # combined "ip:port" field (single colon only, leaves ipv6 alone)
    if not raw.get("port") and ip.count(":") == 1:
        ip, raw["port"] = (x.strip() for x in ip.rsplit(":", 1))
    low = ip.lower()
    if low == "localhost" or low.endswith(".localhost"):
        return None
    if ip and _is_bogus_ip(ip):
        return None
    try:
        port = int(str(raw.get("port") or "").strip())
    except (ValueError, TypeError):
        return None
    if not ip or not (1 <= port <= 65535):
        return None

    rec = {
        "ip": ip,
        "ip_version": ip_version_of(ip),
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
    if raw.get("protocols"):
        p = raw["protocols"]
        if isinstance(p, str):
            p = re.split(r"[,/\s]+", p)
        for x in p:
            protos.extend(parse_protocol(x))
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
    if not rec["country"] and src.get("country"):
        rec["country"] = str(src["country"]).upper()[:2]

    # anonymity / https / response time
    rec["anonymity"] = normalize_anon(raw.get("anonymity") or src.get("anonymity"))
    rec["https"] = to_bool(raw.get("https"))
    rt = raw.get("response_time")
    if isinstance(rt, (int, float)) and rt > 0:
        if src.get("speed_unit") == "s":
            rt = rt * 1000  # upstream reports seconds, store milliseconds
        rec["response_time"] = float(rt)

    # extra metadata, nothing dropped
    meta = {k[len("meta_"):]: v for k, v in raw.items() if k.startswith("meta_") and v not in (None, "")}
    rec["source_meta"] = meta
    # fields the source already verified — checker skips re-checking these
    rec["_provided"] = set(src.get("includes") or [])
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
            spec = ex.get(field)
            if not spec:
                continue
            if isinstance(spec, dict):
                # {"path": dotted, "map": {"raw": "normalized"}}
                v = dig(it, spec.get("path") or "")
                if spec.get("map") is not None:
                    v = spec["map"].get(str(v))
                out[field] = v
            else:
                out[field] = dig(it, spec)
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
    elif decode == "base64_reverse":
        # base64 of the reversed string (proxyverity)
        import base64

        try:
            val = base64.b64decode(str(val)).decode()[::-1]
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
            if isinstance(sel, dict) and "const" in sel:
                out[f] = sel["const"]
            else:
                out[f] = _cell_text(row, sel)
        for mk, sel in (ex.get("source_meta") or {}).items():
            out[f"meta_{mk}"] = _cell_text(row, sel)
        yield out


def parse_content(src, content, defaults=None):
    """parse fetched content per source config -> list of normalized records.
    defaults: per-entry const fields (from urls entries) merged under extracted values"""
    fmt = src.get("format", "text")
    ex = src.get("extract", {})
    proto = src.get("protocol")
    recs = []

    def _merge(r):
        if not defaults:
            return r
        # extracted values win, but empty values never clobber a set default
        return {**defaults, **{k: v for k, v in r.items() if v not in (None, "")}}

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
            rec = _norm_record(_merge(r), src, proto)
            if rec:
                recs.append(rec)

    elif fmt == "text":
        regex = re.compile(ex["regex"])
        if ex.get("finditer"):
            groups = (m.groupdict() for m in regex.finditer(content))
        else:
            groups = (m.groupdict() for m in
                      (regex.match(line.strip()) for line in content.splitlines())
                      if m)
        for g in groups:
            # inline prefix wins over file default
            r = {
                "ip": g.get("ip"),
                "port": g.get("port"),
                "country": g.get("country"),
                "protocol": g.get("proto"),
                "meta_country": g.get("country"),
            }
            rec = _norm_record(_merge(r), src, proto)
            if rec:
                # hideip-style trailing country is a NAME not ISO code
                if g.get("country") and not rec["country"]:
                    rec["country"], rec["country_name"] = "", g["country"]
                    rec["source_meta"].pop("country", None)
                recs.append(rec)

    elif fmt == "html":
        emb = ex.get("embedded_json")
        if emb:
            # json blob inside a <script> tag, parsed with the json rules above
            import json as _json
            from bs4 import BeautifulSoup

            soup = BeautifulSoup(content, "html.parser")
            rx = emb.get("regex")
            doc = None
            for el in soup.select(emb.get("selector", "script")):
                text = (el.string or el.get_text()) if el else ""
                if rx:
                    m = re.search(rx, text, re.S)
                    if not m:
                        continue
                    text = m.group(1)
                try:
                    doc = _json.loads(text.strip())
                    break
                except Exception:
                    continue
            if doc is None:
                return recs
            items = dig(doc, ex.get("root", "")) if ex.get("root") else doc
            if not isinstance(items, list):
                items = [items] if items else []
            for r in _extract_json(items, ex):
                rec = _norm_record(_merge(r), src, proto)
                if rec:
                    recs.append(rec)
            return recs
        for r in _extract_html(content, ex):
            rec = _norm_record(_merge(r), src, proto)
            if rec:
                recs.append(rec)

    return recs


def _run_prefetch(src, session):
    """run prefetch steps; returns (url, extra_headers) for the real fetch.

    each step: { url?, method?, body?, body_type?, regex?, group?, json_path?,
                 header?, as_url?, base? }
    - regex/json_path extract a value from the response
    - header stores it as a request header for subsequent requests
    - as_url feeds it into the next step's url (and the real fetch when the
      last step has it)
    """
    import re as _re

    steps = src.get("prefetch") or []
    headers = {}
    next_url = None
    for step in steps:
        u = step.get("url") or next_url
        content = request(u, method=step.get("method", "GET"),
                          body=step.get("body"),
                          body_type=step.get("body_type"),
                          session=session, headers=headers)
        val = None
        if step.get("json_path"):
            val = dig(_json.loads(content), step["json_path"])
        elif step.get("regex"):
            m = _re.search(step["regex"], content, _re.S)
            val = m.group(step.get("group", 1)) if m else None
        if val is None:
            raise ValueError(f"prefetch step failed for {u}")
        val = str(val).strip()
        if step.get("header"):
            headers[step["header"]] = val
        if step.get("as_url"):
            if step.get("base") and val.startswith("/"):
                val = step["base"] + val
            next_url = val
    url = next_url if (steps and steps[-1].get("as_url")) else src["url"]
    return url, headers


def fetch_source(src, timeout=None, max_pages_default=20, state=None):
    """fetch all entries/pages of a source -> (records, errors[])

    state: optional shared dict for watchdog reporting; updated with
    page / requests / url as the fetch progresses."""
    import time

    timeout = timeout or src.get("timeout", 12)
    if src.get("flow"):
        from .flows import FLOWS

        try:
            return FLOWS[src["flow"]](src)
        except Exception as e:
            return [], [f"{src['name']}: flow '{src['flow']}' failed: {e}"]

    deadline = time.monotonic() + src.get("budget_s", 80)
    recs = []
    errors = []
    pag = src.get("pagination")
    session = make_session(src)
    method = src.get("method", "GET")
    body = src.get("body")
    body_type = src.get("body_type")
    extra_headers = {}

    # multi-entry sources: [{"url": ..., "set": {field: const}}, ...]
    entries = [dict(e) for e in src["urls"]] if src.get("urls") else [{"url": src["url"]}]

    if src.get("prefetch"):
        try:
            entries[0]["url"], extra_headers = _run_prefetch(src, session)
        except Exception as e:
            return [], [f"{src['name']}: prefetch failed: {e}"]

    def fetch_once(url, page_body=None):
        if state is not None:
            state["requests"] = state.get("requests", 0) + 1
            state["url"] = url
        return request(url, method=method,
                       body=body if page_body is None else page_body,
                       body_type=body_type, timeout=timeout, session=session,
                       headers=extra_headers or None)

    for entry in entries:
        url_template = entry["url"]
        defaults = entry.get("set")
        # legacy sources: paginate whenever configured; urls entries: only when templated
        paged = bool(pag) if not src.get("urls") else (
            bool(pag) and ("{page}" in url_template or "{offset}" in url_template))

        if not paged:
            urls = [url_template]
            if entry is entries[0] and src.get("fallback_url"):
                urls.append(src["fallback_url"])
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
                    recs.extend(parse_content(src, content, defaults))
                except Exception as e:
                    errors.append(f"{src['name']}: parse failed: {e}")
            continue

        page = pag.get("start", 1)
        step = pag.get("step", 1)
        max_pages = pag.get("max_pages", max_pages_default)
        delay = pag.get("delay_ms", 0) / 1000.0
        total_path = pag.get("total_path")
        pages_path = pag.get("pages_path")
        limit = pag.get("limit", 100)

        n = 0
        while n < max_pages:
            if time.monotonic() > deadline:
                errors.append(f"{src['name']}: budget exceeded at page {page}")
                break
            url = url_template.replace("{page}", str(page)).replace("{offset}", str(page))
            page_body = body
            if isinstance(body, dict):
                page_body = {k: (page if isinstance(v, str) and v in ("{page}", "{offset}")
                                 else v) for k, v in body.items()}
            if delay:
                time.sleep(delay)
            try:
                # request() already retries 429/5xx/conn errors with backoff
                content = fetch_once(url, page_body)
            except Exception as e:
                errors.append(f"{src['name']} page={page}: fetch failed: {e}")
                break
            # recompute page count from server response on the first page
            if n == 0 and total_path:
                try:
                    total = dig(_json.loads(content), total_path)
                    if total:
                        max_pages = max(1, math.ceil(int(total) / int(limit)))
                except Exception:
                    pass
            elif n == 0 and pages_path:
                try:
                    pages = dig(_json.loads(content), pages_path)
                    if pages:
                        max_pages = min(max_pages, max(1, int(pages)))
                except Exception:
                    pass
            try:
                batch = parse_content(src, content, defaults)
            except Exception as e:
                errors.append(f"{src['name']} page={page}: parse failed: {e}")
                break
            if not batch:
                break
            recs.extend(batch)
            if state is not None:
                state["page"] = page
            page += step
            n += 1

    return recs, errors
