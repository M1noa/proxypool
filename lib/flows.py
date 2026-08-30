"""Multi-step / JS-challenge source flows. Each flow returns (records, errors)."""
import re

from .util import get, request, make_session
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


# ---- cn api flows (proto x anon matrix) -----------------------------------

def _cnapi(src, host):
    errs, raws = [], []
    anons = {"%E9%AB%98%E5%8C%BF": "elite", "%E6%99%AE%E5%8C%BF": "anonymous", "%E9%80%8F%E6%98%8E": "transparent"}
    protos = ("HTTP", "HTTPS", "Socks4", "Socks5")
    import json
    for proto in protos:
        for anon_enc, anon in anons.items():
            try:
                txt = get(f"http://{host}/?num=60&anonymity={anon_enc}&protocol={proto}&format=json&page=1",
                          timeout=src.get("timeout", 20))
                data = json.loads(txt).get("data") or []
                for item in data:
                    if isinstance(item, dict) and item.get("ip"):
                        raws.append({"ip": f"{item['ip']}:{item.get('port')}",
                                     "protocol": proto.lower(), "country": "CN", "anonymity": anon})
            except Exception as e:
                errs.append(f"{src['name']} {proto}/{anon}: {e}")
    return _records(src, raws)[0], errs


def _goodips(src):
    return _cnapi(src, "api.goodips.com")


def _sixsixdaili(src):
    return _cnapi(src, "api.66daili.com//")


# ---- myproxy: html pages, ip:port#CC --------------------------------------

def _myproxy(src):
    errs, raws = [], []
    base = "https://www.my-proxy.com"
    urls = [(f"{base}/free-proxy-list.html" if p == 1 else f"{base}/free-proxy-list-{p}.html", "http", "")
            for p in range(1, min(src.get("max_pages", 10), 10) + 1)]
    urls += [(f"{base}/free-elite-proxy.html", "http", "elite"),
             (f"{base}/free-anonymous-proxy.html", "http", "anonymous"),
             (f"{base}/free-transparent-proxy.html", "http", "transparent"),
             (f"{base}/free-socks-4-proxy.html", "socks4", "elite"),
             (f"{base}/free-socks-5-proxy.html", "socks5", "elite")]
    rx = re.compile(r"(?P<ip>\d{1,3}(?:\.\d{1,3}){3}):(?P<port>\d{1,5})#(?P<country>[A-Z]{2})")
    for url, proto, anon in urls:
        try:
            html = get(url, timeout=src.get("timeout", 20))
            for m in rx.finditer(html):
                raws.append({"ip": f"{m.group('ip')}:{m.group('port')}", "protocol": proto,
                             "country": m.group("country"), "anonymity": anon})
        except Exception as e:
            errs.append(f"myproxy {url}: {e}")
    return _records(src, raws)[0], errs


# ---- iproyal: scrape astro js auth, then cms api ---------------------------

def _iproyal(src):
    errs, raws = [], []
    try:
        sess = make_session(src)
        home = get("https://iproyal.com/", timeout=20, session=sess)
        m = re.search(r'["\'](?P<path>/_astro/FreeProxyListTable\.[^"\']+\.js)["\']', home)
        if not m:
            return [], ["iproyal: astro js not found"]
        js = get("https://iproyal.com" + m.group("path"), timeout=20, session=sess)
        am = re.search(r"Authorization\s*:\s*([\"'])(.*?)\1", js)
        if am:
            sess.headers["Authorization"] = am.group(2)
        sess.headers.update({"Origin": "https://iproyal.com", "Referer": "https://iproyal.com/"})
        import json
        for page in range(1, src.get("max_pages", 3) + 1):
            params = (f"?fields[0]=ip&fields[1]=port&fields[2]=protocol&fields[3]=country"
                      f"&fields[4]=city&pagination[page]={page}&pagination[pageSize]=100")
            try:
                txt = get(f"https://cms.iproyal.com/api/free-proxy-records{params}", timeout=20, session=sess)
                for item in json.loads(txt).get("data") or []:
                    if isinstance(item, dict) and item.get("ip"):
                        raws.append({"ip": f"{item['ip']}:{item.get('port')}", "protocol": str(item.get("protocol", "")).lower(),
                                     "country": item.get("country")})
            except Exception as e:
                errs.append(f"iproyal p{page}: {e}")
    except Exception as e:
        errs.append(f"iproyal: {e}")
    return _records(src, raws)[0], errs


# ---- proxyhub: country links -> tables -------------------------------------

def _proxyhub(src):
    errs, raws = [], []
    try:
        sess = make_session(src)
        home = get("https://proxyhub.me/", timeout=20, session=sess)
        links = set(re.findall(r'href="(/en/[a-z]{2}-free-proxy-list(?:\.html?)?)"', home))
        for link in links:
            try:
                html = get("https://proxyhub.me" + link, timeout=20, session=sess)
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
        html = get("https://www.proxynova.com/proxy-server-list/", timeout=src.get("timeout", 20))
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


# ---- proxyshare: scrape pd token from home --------------------------------

def _proxyshare(src):
    base = src["home"].rstrip("/")
    errs = []
    try:
        home = get(base + "/", timeout=src.get("timeout", 20))
        m = re.search(r'const\s+pd\s*=\s*`([^`]+)`', home)
        if not m:
            return [], ["proxyshare: pd token not found"]
        raws = []
        for sub in ("http", "socks4", "socks5"):
            try:
                txt = request(f"{base}/api/v2/{sub}?key={m.group(1)}", timeout=src.get("timeout", 20))
                for line in txt.splitlines():
                    line = line.strip()
                    if re.match(r"^\d+\.\d+\.\d+\.\d+:\d+$", line):
                        raws.append({"ip": line, "protocol": sub})
            except Exception as e:
                errs.append(f"proxyshare {sub}: {e}")
        return _records(src, raws)[0], errs
    except Exception as e:
        return [], errs + [f"proxyshare: {e}"]


# ---- spysone: inline js port obfuscation ----------------------------------

def _spysone(src):
    if quickjs is None:
        return [], ["spysone: quickjs not installed"]
    errs, raws = [], []
    try:
        html = get(src["home"], timeout=src.get("timeout", 20))
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
    "goodips": _goodips,
    "sixsixdaili": _sixsixdaili,
    "myproxy": _myproxy,
    "iproyal": _iproyal,
    "proxyhub": _proxyhub,
    "proxynova": _proxynova,
    "proxyshare": _proxyshare,
    "spysone": _spysone,
}