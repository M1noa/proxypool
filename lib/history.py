"""sqlite-backed per-protocol proxy state: ema reliability/quality scoring"""
import random
import sqlite3
from datetime import datetime, timedelta, timezone
from pathlib import Path

EMA_ALPHA = 2 / 169      # ~168-check effective window (matches old RECENT_WINDOW)
RECENT_WEIGHT = 0.7      # blend: 0.7*ema + 0.3*all-time
RT_GOOD_MS = 500         # quality 1.0 at or below this
RT_BAD_MS = 8000         # quality speed factor 0.0 at or above this
NEW_PROXY_SCORE = 0.5    # neutral score for proxies with no history

SKIP_FAIL_STREAK = 24    # streak where skip probability maxes out
RECHECK_EVERY_HOURS = 24  # hard floor: never skip if unchecked this long
BASE_SKIP_PROB = 0.15    # skip chance after a single failed check
MAX_SKIP_PROB = 0.9      # always leave a 10% recheck chance

PRUNE_DAYS = 14          # drop proxies not checked within this window, every run

TS_FMT = "%Y-%m-%dT%H:%M:%SZ"
SCHEMA_VERSION = 2

_SCHEMA = """
CREATE TABLE IF NOT EXISTS proxy_state (
    ip              TEXT NOT NULL,
    port            INTEGER NOT NULL,
    proto           TEXT NOT NULL,
    fails_streak    INTEGER NOT NULL DEFAULT 0,
    last_ok_ts      TEXT,
    last_checked_ts TEXT,
    first_seen_ts   TEXT,
    rel_ema         REAL,
    rt_ema          REAL,
    ok_count        INTEGER NOT NULL DEFAULT 0,
    check_count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ip, port, proto)
) WITHOUT ROWID;
"""


class History:
    def __init__(self, path):
        self.db = sqlite3.connect(Path(path))
        v = self.db.execute("PRAGMA user_version").fetchone()[0]
        if v != SCHEMA_VERSION:
            # schema changed: history is derived data, rebuild from scratch
            self.db.executescript(
                "DROP TABLE IF EXISTS checks;"
                "DROP TABLE IF EXISTS proxy_state;")
            self.db.executescript(_SCHEMA)
            self.db.execute(f"PRAGMA user_version={SCHEMA_VERSION}")
            self.db.commit()
        else:
            self.db.executescript(_SCHEMA)
        self.db.execute("PRAGMA journal_mode=WAL")

    def state_map(self):
        """{(ip, port, proto): {fails_streak, last_ok_ts, last_checked_ts,
        first_seen_ts, rel_ema, rt_ema, ok_count, check_count}}"""
        rows = self.db.execute(
            "SELECT ip, port, proto, fails_streak, last_ok_ts, last_checked_ts,"
            " first_seen_ts, rel_ema, rt_ema, ok_count, check_count"
            " FROM proxy_state")
        return {(ip, port, proto): {
            "fails_streak": fs, "last_ok_ts": ok, "last_checked_ts": ck,
            "first_seen_ts": fs_ts, "rel_ema": rel, "rt_ema": rt,
            "ok_count": ocnt, "check_count": ccnt}
            for ip, port, proto, fs, ok, ck, fs_ts, rel, rt, ocnt, ccnt in rows}

    def update_state(self, outcomes, ts, prev):
        """fold this run's per-proto outcomes into proxy_state.

        outcomes: [(ip, port, proto, alive, rt_ms|None)]. prev: state_map from
        before the run. streaks/last-ok, ema reliability, ema response time and
        lifetime counters all advance one step per outcome."""
        rows = []
        for ip, port, proto, alive, rt in outcomes:
            st = prev.get((ip, port, proto), {})
            streak = 0 if alive else st.get("fails_streak", 0) + 1
            last_ok = ts if alive else st.get("last_ok_ts")
            rel = st.get("rel_ema")
            rel = float(alive) if rel is None else rel + EMA_ALPHA * (alive - rel)
            rt_ema = st.get("rt_ema")
            if alive and rt is not None:
                rt_ema = float(rt) if rt_ema is None \
                    else rt_ema + EMA_ALPHA * (rt - rt_ema)
            rows.append((
                ip, port, proto, streak, last_ok, ts,
                st.get("first_seen_ts") or ts, rel, rt_ema,
                st.get("ok_count", 0) + (1 if alive else 0),
                st.get("check_count", 0) + 1))
        self.db.executemany(
            "INSERT INTO proxy_state (ip, port, proto, fails_streak, last_ok_ts,"
            " last_checked_ts, first_seen_ts, rel_ema, rt_ema, ok_count,"
            " check_count) VALUES (?,?,?,?,?,?,?,?,?,?,?)"
            " ON CONFLICT(ip, port, proto) DO UPDATE SET"
            " fails_streak=excluded.fails_streak,"
            " last_ok_ts=excluded.last_ok_ts,"
            " last_checked_ts=excluded.last_checked_ts,"
            " rel_ema=excluded.rel_ema,"
            " rt_ema=excluded.rt_ema,"
            " ok_count=excluded.ok_count,"
            " check_count=excluded.check_count",
            rows)
        self.db.commit()

    def scores(self):
        """{(ip, port, proto): {reliability, quality, checks_total, checks_ok,
        first_seen, last_seen}}"""
        out = {}
        rows = self.db.execute(
            "SELECT ip, port, proto, rel_ema, rt_ema, ok_count, check_count,"
            " first_seen_ts, last_checked_ts FROM proxy_state")
        for ip, port, proto, rel_ema, rt_ema, ok, total, first, last in rows:
            if not total:
                continue
            rel_all = ok / total
            rel = RECENT_WEIGHT * (rel_ema or 0.0) + (1 - RECENT_WEIGHT) * rel_all
            if rt_ema is None:
                speed = NEW_PROXY_SCORE
            else:
                speed = min(1.0, max(
                    0.0, 1.0 - (rt_ema - RT_GOOD_MS) / (RT_BAD_MS - RT_GOOD_MS)))
            out[(ip, port, proto)] = {
                "reliability": round(rel, 4),
                "quality": round(rel * speed, 4),
                "checks_total": total,
                "checks_ok": ok,
                "first_seen": first,
                "last_seen": last,
            }
        return out

    def prune(self, now_ts=None):
        """drop proxies not checked within PRUNE_DAYS, then vacuum."""
        now = parse_ts(now_ts) if now_ts else datetime.now(timezone.utc)
        cutoff = (now - timedelta(days=PRUNE_DAYS)).strftime(TS_FMT)
        n = self.db.execute(
            "DELETE FROM proxy_state WHERE last_checked_ts < ?",
            (cutoff,)).rowcount
        self.db.commit()
        self.db.execute("VACUUM")
        return n

    def close(self):
        self.db.close()


def parse_ts(s):
    return datetime.strptime(s, TS_FMT).replace(tzinfo=timezone.utc)


def skip_keys(records, state, now_ts=None, rng=random):
    """(ip, port, proto) probes to skip this run, chosen probabilistically:
    skip chance ramps from BASE_SKIP_PROB at streak 1 up to MAX_SKIP_PROB
    at SKIP_FAIL_STREAK. never skipped if unchecked for RECHECK_EVERY_HOURS.
    records with no claimed protocols are matched against http/https state."""
    now_ts = now_ts or datetime.now(timezone.utc).strftime(TS_FMT)
    now = parse_ts(now_ts)
    skips = set()
    for r in records:
        claimed = [p for p in (r.get("protocols") or []) if p] \
            or ["http", "https"]
        for proto in claimed:
            st = state.get((r["ip"], r["port"], proto))
            if not st:
                continue
            streak = st["fails_streak"]
            if streak < 1:
                continue
            last = st["last_checked_ts"]
            if last:
                hours = (now - parse_ts(last)).total_seconds() / 3600
                if hours >= RECHECK_EVERY_HOURS:
                    continue
            p = BASE_SKIP_PROB + (MAX_SKIP_PROB - BASE_SKIP_PROB) * min(
                1.0, (streak - 1) / (SKIP_FAIL_STREAK - 1))
            if rng.random() < p:
                skips.add((r["ip"], r["port"], proto))
    return skips
