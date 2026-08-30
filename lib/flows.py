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


# ---- iproyal: static-token cms api ---------------------------------
TOKEN_IPROYAL = ("c07d9ce184008ff4be5ab6afa6a67a7513e5ece56e43b60ad1ddb0b86f952318e"
                 "1ebebf54825bccb6191da8ad135cc29c963ce3f1c46dc4ad8364440333d6bee44"
                 "ae20e3f0e63c29d3c5139c35f84b70d88b4e5de1e2f25cf07dca5d40fa5c0fa09"
                 "3490a5919e3269f2fa853776c59642c50b0cfc761c7f3943edd1908605661")

def _iproyal(src):
    errs, raws = [], []
    headers = {"authorization": f"Bearer {TOKEN_IPROYAL}",
               "origin": "https://iproyal.com", "referer": "https://iproyal.com/"}
    import json
    for page in range(1, src.get("max_pages", 3) + 1):
        params = ("?fields[0]=ip&fields[1]=port&fields[2]=protocol&fields[3]=country"
                  "&fields[4]=city&pagination[page]={page}&pagination[pageSize]=100")
        try:
            txt = get(f"https://cms.iproyal.com/api/free-proxy-records{params.format(page=page)}",
                      timeout=20, headers=headers)
            for item in json.loads(txt).get("data") or []:
                if isinstance(item, dict) and item.get("ip"):
                    raws.append({"ip": f"{item['ip']}:{item.get('port')}",
                                 "protocol": str(item.get("protocol", "")).lower(),
                                 "country": item.get("country")})
        except Exception as e:
            errs.append(f"iproyal p{page}: {e}")
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


# ---- proxyshare: direct no-auth api (proto: 1=Http 2=Https 4=Socks4 8=Socks5) -----
def _proxyshare(src):
    errs, raws = [], []
    proto_map = {"1": "http", "2": "https", "4": "socks4", "8": "socks5"}
    anon_map = {"2": "elite", "1": "anonymous", "0": "transparent"}
    url = "https://www.proxyshare.com/fetch-proxy/free?page_size=15000&page=1&language=en-us"
    try:
        import json
        txt = get(url, timeout=src.get("timeout", 30))
        data = json.loads(txt).get("data") or {}
        for page in range(1, min(data.get("page_count", 1), src.get("max_pages", 10)) + 1):
            if page > 1:
                txt = get(f"https://www.proxyshare.com/fetch-proxy/free?page_size=15000&page={page}&language=en-us",
                          timeout=src.get("timeout", 30))
                data = json.loads(txt).get("data") or {}
            for item in data.get("list") or []:
                proto = proto_map.get(str(item.get("protocol")))
                if not proto:
                    continue
                ip, port = item.get("ip"), item.get("port")
                if ip and port:
                    raws.append({"ip": f"{ip}:{port}", "protocol": proto,
                                 "country": item.get("country_code"),
                                 "anonymity": anon_map.get(str(item.get("anonymity")))})
    except Exception as e:
        errs.append(f"proxyshare: {e}")
    return _records(src, raws)[0], errs

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