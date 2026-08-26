"""db-ip lite country mmdb, downloaded fresh each run"""
import datetime as dt
from pathlib import Path

BASE = "https://download.db-ip.com/free/dbip-country-lite-{ym}.mmdb.gz"


def download_mmdb(cache_dir):
    """returns path to cached mmdb, downloading current-month db (prev-month fallback)"""
    dest = Path(cache_dir) / "dbip-country-lite.mmdb"
    if dest.exists():
        return dest
    import requests

    today = dt.date.today()
    prev = today.replace(day=1) - dt.timedelta(days=1)
    for d in (today, prev):
        ym = f"{d.year}-{d.month:02d}"
        try:
            r = requests.get(BASE.format(ym=ym), timeout=180,
                             headers={"user-agent": "proxypool/1.0"})
            if r.status_code == 200 and r.content[:2] == b"\x1f\x8b":
                import gzip

                raw = gzip.decompress(r.content)
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_bytes(raw)
                return dest
        except Exception:
            continue
    raise RuntimeError("could not download db-ip lite mmdb")


class GeoIP:
    def __init__(self, path):
        import maxminddb

        self.reader = maxminddb.open_database(str(path))

    def country(self, ip):
        try:
            data = self.reader.get(ip)
        except Exception:
            return ""
        if isinstance(data, dict):
            code = (data.get("country") or {}).get("iso_code")
            if isinstance(code, str) and len(code) == 2:
                return code.upper()
        return ""
