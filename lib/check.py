"""async proxy checker: protocol probes, https, anonymity, calibrated response time"""
import asyncio
import time
from datetime import datetime, timezone

import aiohttp

CHECK_URL = "http://www.google.com/generate_204"
ECHO_URLS = ["http://azenv.net/", "http://httpbin.org/get"]
MYIP_URLS = ["https://api.ipify.org", "https://icanhazip.com"]
BASELINE_PINGS = 5
CONCURRENCY = 512
# all proxies ping the same endpoint (google generate_204); 6s keeps the run
# moving — most dead proxies hang until timeout, and 74k of them add up fast
TIMEOUT = 6

ANON_KEYS = ("via", "x-forwarded-for", "forwarded", "client-ip")


def _timeout():
    return aiohttp.ClientTimeout(total=TIMEOUT)


async def _fetch(session, url, proxy=None):
    async with session.get(url, proxy=proxy) as resp:
        resp.raise_for_status()
        return await resp.read()


async def _socks_fetch(ip, port, proto, url):
    from aiohttp_socks import ProxyConnector, ProxyType

    pt = ProxyType.SOCKS5 if proto == "socks5" else ProxyType.SOCKS4
    connector = ProxyConnector(proxy_type=pt, host=ip, port=int(port))
    try:
        async with aiohttp.ClientSession(connector=connector, timeout=_timeout()) as s:
            return await _fetch(s, url)
    finally:
        await connector.close()


class Checker:
    def __init__(self):
        self.baseline = 0.0
        self.my_ip = ""

    def _calibrated(self, elapsed_ms):
        return max(1, round(elapsed_ms - self.baseline))

    async def calibrate(self):
        """ping check endpoint directly; baseline = fastest round trip"""
        times = []
        async with aiohttp.ClientSession(timeout=_timeout()) as s:
            for _ in range(BASELINE_PINGS):
                t0 = time.monotonic()
                try:
                    await _fetch(s, CHECK_URL)
                    times.append((time.monotonic() - t0) * 1000)
                except Exception:
                    continue
            if not times:
                raise RuntimeError("cannot reach check endpoint for baseline")
            self.baseline = min(times)
            for u in MYIP_URLS:
                try:
                    self.my_ip = (await _fetch(s, u)).decode().strip()
                    if self.my_ip:
                        break
                except Exception:
                    continue

    async def _probe(self, ip, port, probe):
        """returns calibrated ms or raises"""
        t0 = time.monotonic()
        if probe == "https":
            # CONNECT tunnel through the http proxy to an https target
            async with aiohttp.ClientSession(timeout=_timeout()) as s:
                await _fetch(s, "https://www.google.com/generate_204",
                             proxy=f"http://{ip}:{port}")
        elif probe == "http":
            async with aiohttp.ClientSession(timeout=_timeout()) as s:
                await _fetch(s, CHECK_URL, proxy=f"http://{ip}:{port}")
        else:  # socks4 / socks5
            await _socks_fetch(ip, port, probe, CHECK_URL)
        return (time.monotonic() - t0) * 1000

    def _probe_plan(self, rec):
        """probes to run; trusted means source already verified protocols (rt only)"""
        claimed = [p for p in rec["protocols"] if p]
        provided = rec.get("_provided") or ()
        if "protocols" in provided and claimed:
            # measure rt through the first claimed protocol only
            first = claimed[0]
            return [first], True
        probes = []
        socks = [p for p in ("socks4", "socks5") if p in claimed]
        if socks:
            probes.extend(socks)
        else:
            # unknown or http-claimed: test CONNECT first (success => http+https)
            probes.append("https")
            probes.append("http")
        return probes, False

    @staticmethod
    def _classify_anon(text, my_ip):
        low = text.lower()
        if my_ip and my_ip in text:
            return "transparent"
        if any(k in low for k in ANON_KEYS):
            return "anonymous"
        return "elite"

    async def _echo_anonymity(self, ip, port):
        for u in ECHO_URLS:
            try:
                async with aiohttp.ClientSession(timeout=_timeout()) as s:
                    raw = await _fetch(s, u, proxy=f"http://{ip}:{port}")
                return self._classify_anon(raw.decode(errors="replace"), self.my_ip)
            except Exception:
                continue
        return ""

    async def _check_one(self, sem, rec):
        async with sem:
            probes, trusted = self._probe_plan(rec)
            best_rt = None
            best_raw = None
            ok = set()
            for p in probes:
                try:
                    ms = await self._probe(rec["ip"], rec["port"], p)
                except Exception:
                    continue
                rt = self._calibrated(ms)
                raw = round(ms)
                if best_rt is None or rt < best_rt:
                    best_rt = rt
                    best_raw = raw
                ok.add("https" if p == "https" else p)
                if p == "https":
                    ok.add("http")
            dead = not ok
            if trusted and not dead:
                final = sorted(set(rec["protocols"]))  # keep full claim
            elif dead:
                return False
            else:
                final = sorted(ok)
            if final:
                rec["protocols"] = final
            if best_rt is not None:
                rec["response_time_ms"] = int(best_rt)
                rec["response_time_raw_ms"] = int(best_raw)
            if "https" in rec["protocols"]:
                rec["https"] = True
            if rec["anonymity"] == "" and "anonymity" not in (rec.get("_provided") or ()) \
                    and "http" in rec["protocols"]:
                rec["anonymity"] = await self._echo_anonymity(rec["ip"], rec["port"])
            return True


async def _progress_reporter(total, counters, interval=10):
    """every `interval`s log checked/rate/alive/dead/elapsed/eta/cpu/net"""
    import os
    import time as _t

    try:
        import psutil
    except ImportError:
        psutil = None
    t0 = _t.monotonic()
    last_checked = 0
    last_net = psutil.net_io_counters() if psutil else None
    if psutil:
        psutil.cpu_percent(None)  # prime; first call is meaningless
    while True:
        await asyncio.sleep(interval)
        elapsed = _t.monotonic() - t0
        checked = counters["checked"]
        rate = (checked - last_checked) / interval
        last_checked = checked
        remaining = total - checked
        eta_s = remaining / rate if rate > 0 else float("inf")
        eta = f"{eta_s / 60:.1f}min" if eta_s != float("inf") else "--"
        if psutil:
            cpu = f"{psutil.cpu_percent(None):.0f}%"
            io = psutil.net_io_counters()
            d = ((io.bytes_sent + io.bytes_recv)
                 - (last_net.bytes_sent + last_net.bytes_recv))
            net = f" net {d / interval / 1e6:.1f}MB/s"
            last_net = io
        else:
            cpu = f"load {os.getloadavg()[0]:.1f}"
            net = ""
        print(f"[check {elapsed:6.0f}s] {checked}/{total} "
              f"({rate:.0f}/s) alive={counters['alive']} "
              f"dead={counters['dead']} eta={eta} cpu={cpu}{net}",
              flush=True)


async def check_all(records, concurrency=CONCURRENCY, skip=()):
    """in place: verifies protocols, fills anonymity/https/RT/last_checked.
    skip: iterable of (ip, port) known-dead proxies to not probe.
    returns (alive_records, stats, outcomes, skipped_count) — dead proxies are
    dropped from alive_records; outcomes is [(ip, port, alive, rt_ms|None)]
    for everything actually checked"""
    skip_set = set(skip)
    if skip_set:
        before = len(records)
        records = [r for r in records
                   if (r["ip"], r["port"]) not in skip_set]
        skipped = before - len(records)
    else:
        skipped = 0
    c = Checker()
    await c.calibrate()
    sem = asyncio.Semaphore(concurrency)

    counters = {"checked": 0, "alive": 0, "dead": 0}

    async def run(rec):
        try:
            ok = await c._check_one(sem, rec)
        except Exception:
            ok = False
        counters["checked"] += 1
        counters["alive" if ok else "dead"] += 1
        return ok

    reporter = asyncio.ensure_future(
        _progress_reporter(len(records), counters))
    try:
        results = await asyncio.gather(*[run(r) for r in records])
    finally:
        reporter.cancel()
    outcomes = [
        (r["ip"], r["port"], ok, r.get("response_time_ms") if ok else None)
        for r, ok in zip(records, results)
    ]
    alive = [r for r, ok in zip(records, results) if ok]
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    for r in alive:
        r["last_checked"] = now
        r.pop("_provided", None)
    stats = {
        "total": len(records),
        "alive": len(alive),
        "dead": len(records) - len(alive),
        "skipped": skipped,
        "baseline_ms": round(c.baseline),
    }
    return alive, stats, outcomes, skipped
