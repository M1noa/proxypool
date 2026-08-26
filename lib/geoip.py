"""db-ip lite country + asn mmdb, downloaded fresh each run"""
import datetime as dt
from pathlib import Path

BASE = "https://download.db-ip.com/free/dbip-country-lite-{ym}.mmdb.gz"
ASN_BASE = "https://download.db-ip.com/free/dbip-asn-lite-{ym}.mmdb.gz"

# asn org names that indicate hosting/datacenter/cloud infrastructure
HOSTING_KEYWORDS = (
    "amazon", "aws", "azure", "microsoft", "google", "oracle",
    "alibaba", "tencent cloud", "ovh", "hetzner", "digitalocean",
    "linode", "akamai", "cloudflare", "fastly", "vultr", "choopa",
    "leaseweb", "contabo", "m247", "datacamp", "hostinger", "godaddy",
    "scaleway", "online s.a.s", "iliad", "hivelocity", "phoenixnap",
    "quadranet", "psychz", "rackspace", "equinix", "melbikomas",
    "aeza", "flokinet", "stark industries", "gthost", "colocation",
    "datacenter", "data center", "hosting", "server", "vps", "dedicated",
)


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
    """db-ip asn lite: asn number + org name per ip, plus hosting/residential guess"""

    def __init__(self, path):
        import maxminddb

        self.reader = maxminddb.open_database(str(path))

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
        low = org.lower()
        ip_type = "hosting" if any(k in low for k in HOSTING_KEYWORDS) else "residential"
        return {"asn": num, "as_org": org, "ip_type": ip_type}
