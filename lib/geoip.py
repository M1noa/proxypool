"""db-ip lite country + asn mmdb, downloaded fresh each run"""
import datetime as dt
import json
from pathlib import Path

BASE = "https://download.db-ip.com/free/dbip-country-lite-{ym}.mmdb.gz"
ASN_BASE = "https://download.db-ip.com/free/dbip-asn-lite-{ym}.mmdb.gz"
# cc0, updated daily: asn -> category (isp/hosting/business/education_research/government_admin)
ASN_META_URL = "https://raw.githubusercontent.com/ipverse/as-metadata/master/as.json"


def _download(base, dest_name, cache_dir):
    """returns path to cached mmdb, downloading current-month db (prev-month fallback)"""
    import requests

    dest = Path(cache_dir) / dest_name
    if dest.exists():
        return dest
    today = dt.date.today()
    prev = today.replace(day=1) - dt.timedelta(days=1)
    for d in (today, prev):
        ym = f"{d.year}-{d.month:02d}"
        try:
            r = requests.get(base.format(ym=ym), timeout=180,
                             headers={"user-agent": "proxypool/1.0"})
            if r.status_code == 200 and r.content[:2] == b"\x1f\x8b":
                import gzip

                raw = gzip.decompress(r.content)
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_bytes(raw)
                return dest
        except Exception:
            continue
    raise RuntimeError(f"could not download {dest_name}")


def download_mmdb(cache_dir):
    return _download(BASE, "dbip-country-lite.mmdb", cache_dir)


def download_asn_mmdb(cache_dir):
    return _download(ASN_BASE, "dbip-asn-lite.mmdb", cache_dir)


def download_asn_categories(cache_dir):
    """asn -> category dict from ipverse as-metadata (cc0)"""
    dest = Path(cache_dir) / "ipverse-as-categories.json"
    if not dest.exists():
        import requests

        r = requests.get(ASN_META_URL, timeout=300,
                         headers={"user-agent": "proxypool/1.0"})
        r.raise_for_status()
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(r.content)
    categories = {}
    for entry in json.loads(dest.read_text()):
        cat = (entry.get("metadata") or {}).get("category")
        if cat:
            categories[entry["asn"]] = cat
    return categories


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


class AsnDB:
    """db-ip asn lite: asn number + org name per ip, typed via ipverse category"""

    def __init__(self, path, categories=None):
        import maxminddb

        self.reader = maxminddb.open_database(str(path))
        self.categories = categories or {}

    def lookup(self, ip):
        try:
            data = self.reader.get(ip)
        except Exception:
            data = None
        num, org = None, ""
        if isinstance(data, dict):
            n = data.get("autonomous_system_number")
            if isinstance(n, int):
                num = n
            o = data.get("autonomous_system_organization")
            if isinstance(o, str):
                org = o
        ip_type = "hosting" if self.categories.get(num) == "hosting" else "residential"
        return {"asn": num, "as_org": org, "ip_type": ip_type,
                "asn_category": self.categories.get(num) or ""}
