"""Multi-step / JS-challenge source flows. Each flow returns (records, errors)."""
import re

from .util import get, request
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


def _proxynova(src):
    if quickjs is None:
        return [], ["proxynova: quickjs not installed"]
    errs, raws = [], []
    for page in range(1, src.get("max_pages", 5) + 1):
        try:
            html = get(f"{src['home'].rstrip('/')}/proxy-server-list/page-{page}/", timeout=src.get("timeout", 20))
            for m in re.finditer(r"document\.getElementById\(['\"]pp['\"]\)\.innerHTML\s*=\s*atob\(['\"]([^'\"]+)", html):
                import base64
                frag = base64.b64decode(m.group(1)).decode("utf-8", "ignore")
                ipm = re.search(r"(\d+\.\d+\.\d+\.\d+)", frag)
                if ipm:
                    raws.append({"ip": ipm.group(1)})
            for m in re.finditer(r"addItem\(['\"]([^'\"]+)['\"],\s*(\d+)", html):
                raws.append({"ip": f"{m.group(1)}:{m.group(2)}", "protocol": "http"})
            if not raws:
                break
        except Exception as e:
            errs.append(f"proxynova p{page}: {e}")
            break
    recs, errs2 = _records(src, raws)
    return recs, errs + errs2


def _spysone(src):
    if quickjs is None:
        return [], ["spysone: quickjs not installed"]
    errs, raws = [], []
    try:
        html = get(src["home"], timeout=src.get("timeout", 20))
        # spysone builds the port via small inline js exprs like +(x^y) per row
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
    "proxyshare": _proxyshare,
    "proxynova": _proxynova,
    "spysone": _spysone,
}
