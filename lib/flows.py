"""Multi-step / JS-challenge source flows. Each flow returns (records, errors).

Kept as code because config can't express them cleanly:
- sixsixdaili: 12-combo proto x anonymity matrix against one api
- proxyhub: link discovery from homepage, then per-country tables
- proxynova: script-noise html table
- spysone: inline js port obfuscation (needs quickjs)
"""
import re

from .util import get, make_session
from .parse import _norm_record

try:
    import quickjs  # optional; needed for js-obfuscated sources
except ImportError:
    quickjs = None


def _records(src, raws):
    out, errs = [], []
    for r in raws:
        try:
            rec = _norm_record(r, src, src.get("protocol"))
            if rec:
                out.append(rec)
        except Exception as e:
            errs.append(str(e))
    return out, errs


def _clean(td):
    return re.sub(r"\s+", " ", re.sub(r"<[^>]+>", "", td)).strip()


def _remain(src):
    d = src.get("_deadline")
    return max(0.0, d - __import__("time").monotonic()) if d else 60.0


def _get(src, url, **kw):
    """get with the source budget deadline + watchdog progress reporting"""
    st = src.get("_state")
    if st is not None:
        st["requests"] = st.get("requests", 0) + 1
        st["url"] = url
    kw.setdefault("timeout", min(src.get("timeout", 20), max(1.0, _remain(src))))
    return get(url, deadline=src.get("_deadline"), **kw)


# ---- 66daili: cn api, proto x anonymity matrix ----------------------------

def _sixsixdaili(src):
    errs, raws = [], []
    anons = {"%E9%AB%98%E5%8C%BF": "elite", "%E6%99%AE%E5%8C%BF": "anonymous", "%E9%80%8F%E6%98%8E": "transparent"}
    import json
    for proto in ("HTTP", "HTTPS", "Socks4", "Socks5"):
        for anon_enc, anon in anons.items():
            if _remain(src) <= 0:
                errs.append(f"{src['name']}: budget exceeded")
                return _records(src, raws)[0], errs
            try:
                txt = _get(src, f"http://api.66daili.com//?num=60&anonymity={anon_enc}&protocol={proto}&format=json&page=1")
                data = json.loads(txt).get("data") or []
                for item in data:
                    if isinstance(item, dict) and item.get("ip"):
                        raws.append({"ip": f"{item['ip']}:{item.get('port')}",
                                     "protocol": proto.lower(), "country": "CN", "anonymity": anon})
            except Exception as e:
                errs.append(f"{src['name']} {proto}/{anon}: {e}")
    return _records(src, raws)[0], errs


# ---- proxyhub: country links -> tables -------------------------------------

def _proxyhub(src):
    errs, raws = [], []
    try:
        sess = make_session(src)
        home = _get(src, "https://proxyhub.me/", session=sess)
        links = set(re.findall(r'href="(/en/[a-z]{2}-free-proxy-list(?:\.html?)?)"', home))
        for link in links:
            if _remain(src) <= 0:
                errs.append("proxyhub: budget exceeded")
                break
            try:
                html = _get(src, "https://proxyhub.me" + link, session=sess)
                country = re.search(r"/en/([a-z]{2})-free-proxy-list", link).group(1).upper()
                for tr in re.findall(r"<tr[^>]*>(.*?)</tr>", html, re.S):
                    tds = re.findall(r"<td[^>]*>(.*?)</td>", tr, re.S)
                    if len(tds) < 5:
                        continue
                    ip = _clean(tds[1])
                    port = _clean(tds[2])
                    proto = _clean(tds[3]).lower()
                    anon = _clean(tds[4]).lower()
                    if re.match(r"^\d{1,3}(?:\.\d{1,3}){3}$", ip) and port:
                        raws.append({"ip": f"{ip}:{port}", "protocol": proto, "country": country, "anonymity": anon})
            except Exception as e:
                errs.append(f"proxyhub {link}: {e}")
    except Exception as e:
        errs.append(f"proxyhub: {e}")
    return _records(src, raws)[0], errs


# ---- proxynova: homepage table ---------------------------------------------

def _proxynova(src):
    errs, raws = [], []
    try:
        html = _get(src, "https://www.proxynova.com/proxy-server-list/")
        for tr in re.findall(r"<tr[^>]*>(.*?)</tr>", html, re.S):
            tds = re.findall(r"<td[^>]*>(.*?)</td>", tr, re.S)
            if len(tds) < 7:
                continue
            td0 = re.sub(r"<script.*?</script>", "", tds[0], flags=re.S)
            ipm = re.search(r"(\d{1,3}(?:\.\d{1,3}){3})", td0)
            port = _clean(tds[1])
            anon = _clean(tds[6]).lower()
            if ipm and port and re.match(r"^\d{1,5}$", port):
                raws.append({"ip": f"{ipm.group(1)}:{port}", "protocol": "http", "anonymity": anon})
    except Exception as e:
        errs.append(f"proxynova: {e}")
    return _records(src, raws)[0], errs


# ---- spysone: inline js port obfuscation ----------------------------------

def _spysone(src):
    if quickjs is None:
        return [], ["spysone: quickjs not installed"]
    errs, raws = [], []
    try:
        html = _get(src, src["home"])
        for tr in re.findall(r"<tr[^>]*>(.*?)</tr>", html, re.S):
            ipm = re.search(r"(\d+\.\d+\.\d+\.\d+)", tr)
            pm = re.search(r"document\.write\(['\"]:['\"]\s*\+(.*?)\)</script>", tr)
            if not ipm or not pm:
                continue
            expr = re.sub(r"[()\s]", "", pm.group(1))
            try:
                port = int(quickjs.eval(expr))
            except Exception:
                continue
            raws.append({"ip": f"{ipm.group(1)}:{port}", "protocol": src.get("protocol", "http")})
    except Exception as e:
        errs.append(f"spysone: {e}")
    recs, errs2 = _records(src, raws)
    return recs, errs + errs2


FLOWS = {
    "sixsixdaili": _sixsixdaili,
    "proxyhub": _proxyhub,
    "proxynova": _proxynova,
    "spysone": _spysone,
}
