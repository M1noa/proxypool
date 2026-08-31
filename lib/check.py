"""async proxy checker: protocol probes, https, anonymity, calibrated response time"""
import asyncio
import time
from datetime import datetime, timezone

import aiohttp

CHECK_URL = "http://www.google.com/generate_204"
ECHO_URLS = ["http://azenv.net/", "http://httpbin.org/get"]
MYIP_URLS = ["https://api.ipify.org", "https://icanhazip.com"]
BASELINE_PINGS = 5
CONCURRENCY = 1024  # fallback when specs can't be read
CONCURRENCY_MIN = 768
CONCURRENCY_MAX = 2400
SPEEDTEST_URL = "https://speed.cloudflare.com/__down?bytes=10000000"
SPEEDTEST_CONN = 4
# all proxies ping the same endpoint (google generate_204); 5s keeps the run
# moving — most dead proxies hang until timeout, and they add up fast
TIMEOUT = 5

ANON_KEYS = ("via", "x-forwarded-for", "forwarded", "client-ip")


def _timeout():
    return aiohttp.ClientTimeout(total=TIMEOUT)


async def _fetch(session, url, proxy=None):
    async with session.get(url, proxy=proxy) as resp:
        resp.raise_for_status()
        return await resp.read()


def _derive_concurrency(mbps):
    """worker count from runner specs weighed against measured bandwidth.
    the checker is latency-bound (a full run barely moves 10 mbps), so cpu and
    free ram set the real ceiling; bandwidth only pulls the number down on a
    thin pipe. each dimension gives a worker budget, blended then clamped."""
    import os

    cpus = os.cpu_count() or 2
    cpu_budget = cpus * 600  # event loop + tls handshakes one core can absorb
    try:
        import psutil
        # available, not total: the record set is already resident by now
        ram_budget = (psutil.virtual_memory().available / 1e9) * 500
    except Exception:
        ram_budget = CONCURRENCY
    net_budget = mbps * 4 if mbps else cpu_budget
    blend = 0.45 * cpu_budget + 0.25 * ram_budget + 0.30 * net_budget
    if not blend:
        return CONCURRENCY
    return int(max(CONCURRENCY_MIN, min(CONCURRENCY_MAX, round(blend))))


def _raise_fd_limit(concurrency):
    """each worker can hold a gate socket plus one per parallel probe; the
    default soft limit is well under that. raise toward the hard limit."""
    try:
        import resource
        soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
        want = min(hard, max(soft, concurrency * 6 + 1024))
        if want > soft:
            resource.setrlimit(resource.RLIMIT_NOFILE, (want, hard))
    except Exception:
        pass


async def _tcp_open(ip, port):
    """true if the port accepts a connection within TIMEOUT. a multi-protocol
    claim that fails this once is skipped entirely instead of paying TIMEOUT
    again per claimed protocol."""
    try:
        reader, writer = await asyncio.wait_for(
            asyncio.open_connection(ip, int(port)), TIMEOUT)
    except Exception:
        return False
    writer.close()
    try:
        await writer.wait_closed()
    except Exception:
        pass
    return True


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
        self.mbps = 0.0

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

    async def measure_mbps(self):
        """multithreaded speedtest: parallel downloads, aggregate throughput"""
        async def one(session):
            async with session.get(SPEEDTEST_URL) as r:
                r.raise_for_status()
                return len(await r.read())

        t0 = time.monotonic()
        try:
            async with aiohttp.ClientSession() as s:
                sizes = await asyncio.wait_for(
                    asyncio.gather(*[one(s) for _ in range(SPEEDTEST_CONN)]),
                    timeout=30)
        except Exception:
            return 0.0
        dt = time.monotonic() - t0
        self.mbps = (sum(sizes) * 8 / 1e6) / dt if dt > 0 else 0.0
        return self.mbps

    async def _probe(self, ip, port, probe, session):
        """returns calibrated ms or raises"""
        t0 = time.monotonic()

        async def do():
            if probe == "https":
                # CONNECT tunnel through the http proxy to an https target
                await _fetch(session, "https://www.google.com/generate_204",
                             proxy=f"http://{ip}:{port}")
            elif probe == "http":
                await _fetch(session, CHECK_URL, proxy=f"http://{ip}:{port}")
            else:  # socks4 / socks5
                await _socks_fetch(ip, port, probe, CHECK_URL)

        # the socks handshake / CONNECT tunnel can hang past aiohttp's own
        # timeout on a peer that accepts the TCP connection but never speaks
        # the protocol — a hard wait_for guarantees this coroutine returns,
        # so one dead proxy can't stall the whole worker pool's gather()
        await asyncio.wait_for(do(), TIMEOUT + 2)
        return (time.monotonic() - t0) * 1000

    @staticmethod
    def _probe_plan(rec):
        """every claimed protocol gets probed; http+https both tested
        (CONNECT + plain). unknown claims get the https/http discovery pair."""
        claimed = [p for p in rec["protocols"] if p]
        probes = [p for p in ("socks4", "socks5") if p in claimed]
        if "http" in claimed or "https" in claimed:
            probes += ["https", "http"]
        return probes or ["https", "http"]

    @staticmethod
    def _classify_anon(text, my_ip):
        low = text.lower()
        if my_ip and my_ip in text:
            return "transparent"
        if any(k in low for k in ANON_KEYS):
            return "anonymous"
        return "elite"

    async def _echo_anonymity(self, ip, port, session):
        for u in ECHO_URLS:
            try:
                raw = await _fetch(session, u, proxy=f"http://{ip}:{port}")
                return self._classify_anon(raw.decode(errors="replace"), self.my_ip)
            except Exception:
                continue
        return ""

    async def _check_one(self, rec, session):
        plan = rec.get("_plan") or self._probe_plan(rec)
        if not await _tcp_open(rec["ip"], rec["port"]):
            rec["_probes"] = {p: None for p in plan}
            return False
        # probes run concurrently: a dead proxy costs one TIMEOUT total, not
        # one per claimed protocol (which was 10-20s a record)
        got = await asyncio.gather(
            *(self._probe(rec["ip"], rec["port"], p, session) for p in plan),
            return_exceptions=True)
        best_rt = None
        best_raw = None
        ok = set()
        probes = {}
        for p, ms in zip(plan, got):
            if isinstance(ms, BaseException):
                probes[p] = None
                continue
            rt = self._calibrated(ms)
            raw = round(ms)
            probes[p] = rt
            if best_rt is None or rt < best_rt:
                best_rt = rt
                best_raw = raw
            ok.add(p)
            if p == "https":
                ok.add("http")
        rec["_probes"] = probes
        if not ok:
            return False
        rec["protocols"] = sorted(ok)
        if best_rt is not None:
            rec["response_time_ms"] = int(best_rt)
            rec["response_time_raw_ms"] = int(best_raw)
        if "https" in rec["protocols"]:
            rec["https"] = True
        if rec["anonymity"] == "" and "anonymity" not in (rec.get("_provided") or ()) \
                and "http" in rec["protocols"]:
            rec["anonymity"] = await self._echo_anonymity(rec["ip"], rec["port"], session)
        return True


async def _progress_reporter(total, counters, interval=10, concurrency=0):
    """every `interval`s log checked/avg-rate/alive/dead/elapsed/eta/cpu/net.
    eta uses the cumulative average rate over the whole run, so it gets more
    accurate as more data comes in."""
    import os
    import time as _t

    try:
        import psutil
    except ImportError:
        psutil = None
    t0 = _t.monotonic()
    last_net = psutil.net_io_counters() if psutil else None
    if psutil:
        psutil.cpu_percent(None)  # prime; first call is meaningless
    while True:
        await asyncio.sleep(interval)
        elapsed = _t.monotonic() - t0
        checked = counters["checked"]
        rate = checked / elapsed if elapsed else 0
        remaining = total - checked
        eta_s = remaining / rate if rate > 0 else float("inf")
        eta = f"{eta_s / 60:.1f}min" if eta_s != float("inf") else "--"
        if psutil:
            cpu = f"{psutil.cpu_percent(None):.0f}%"
            io = psutil.net_io_counters()
            d = ((io.bytes_sent + io.bytes_recv)
                 - (last_net.bytes_sent + last_net.bytes_recv))
            net = f" net {d / interval * 8 / 1e6:.0f}mbps"
            last_net = io
        else:
            cpu = f"load {os.getloadavg()[0]:.1f}"
            net = ""
        print(f"[check {elapsed:6.0f}s] {checked}/{total} "
              f"({rate:.0f}/s avg) alive={counters['alive']} "
              f"dead={counters['dead']} eta={eta} cpu={cpu}{net} conc={concurrency}",
              flush=True)


async def check_all(records, concurrency=0, skip=(), speedtest=True, prev_alive=()):
    """in place: verifies protocols, fills anonymity/https/RT/last_checked.
    skip: iterable of (ip, port, proto) known-dead protocol probes to skip;
    a record is only dropped when every probe in its plan is skipped.
    prev_alive: (ip, port) pairs with a history success — failures among them
    get one second-chance re-probe at the end.
    concurrency 0 = derive from runner specs + measured bandwidth, see
    _derive_concurrency.
    returns (alive_records, stats, outcomes, skipped_count) — dead proxies are
    dropped from alive_records; outcomes is [(ip, port, proto, alive,
    rt_ms|None)] for every protocol actually probed"""
    import random

    skip_set = set(skip)
    skipped = 0
    if skip_set:
        kept = []
        for r in records:
            plan = [p for p in Checker._probe_plan(r)
                    if (r["ip"], r["port"], p) not in skip_set]
            if plan:
                r["_plan"] = plan
                kept.append(r)
            else:
                skipped += 1
        records = kept
    # shuffle so dead-heavy source clusters don't skew the running rate/eta
    random.shuffle(records)
    c = Checker()
    await c.calibrate()
    if not concurrency:
        mbps = await c.measure_mbps() if speedtest else 0.0
        concurrency = _derive_concurrency(mbps)
    import os as _os
    _spec = f"{_os.cpu_count() or '?'}cpu"
    try:
        import psutil as _ps
        _spec += f" {_ps.virtual_memory().available / 1e9:.1f}gb free"
    except Exception:
        pass
    print(f"concurrency={concurrency} ({_spec}"
          + (f", {c.mbps:.0f} mbps)" if c.mbps else ", no speedtest)"),
          flush=True)

    _raise_fd_limit(concurrency)
    counters = {"checked": 0, "alive": 0, "dead": 0}
    results = [False] * len(records)
    connector = aiohttp.TCPConnector(limit=0, ttl_dns_cache=300)
    session = aiohttp.ClientSession(connector=connector, timeout=_timeout())

    async def run_pool(pool_records, out, track=None):
        """worker pool: `concurrency` workers pull indices off a shared counter"""
        it = 0

        async def worker():
            nonlocal it
            while True:
                i = it
                it += 1
                if i >= len(pool_records):
                    return
                rec = pool_records[i]
                try:
                    ok = await c._check_one(rec, session)
                except Exception:
                    ok = False
                out[i] = ok
                if track is not None:
                    track["checked"] += 1
                    track["alive" if ok else "dead"] += 1

        await asyncio.gather(*(worker() for _ in range(concurrency)))

    reporter = asyncio.ensure_future(
        _progress_reporter(len(records), counters, concurrency=concurrency))
    try:
        try:
            await run_pool(records, results, counters)
        finally:
            reporter.cancel()

        # second chance: recently-alive proxies that just failed get one re-probe
        revived = 0
        prev = set(prev_alive or ())
        retry_idx = [i for i, r in enumerate(records)
                     if not results[i] and (r["ip"], r["port"]) in prev]
        if retry_idx:
            print(f"second-chance: re-probing {len(retry_idx)} recently-alive "
                  f"failures", flush=True)
            retry = [records[i] for i in retry_idx]
            rres = [False] * len(retry)
            await run_pool(retry, rres)
            for j, ok in enumerate(rres):
                if ok:
                    results[retry_idx[j]] = True
                    revived += 1
            print(f"second-chance: revived {revived}/{len(retry_idx)}", flush=True)
    finally:
        await session.close()

    outcomes = [
        (r["ip"], r["port"], proto, rt is not None, rt)
        for r in records
        for proto, rt in (r.get("_probes") or {}).items()
    ]
    alive = [r for r, ok in zip(records, results) if ok]
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    for r in alive:
        r["last_checked"] = now
        r.pop("_provided", None)
    for r in records:
        r.pop("_plan", None)
        r.pop("_probes", None)
    stats = {
        "total": len(records),
        "alive": len(alive),
        "dead": len(records) - len(alive),
        "skipped": skipped,
        "revived": revived,
        "baseline_ms": round(c.baseline),
    }
    return alive, stats, outcomes, skipped
