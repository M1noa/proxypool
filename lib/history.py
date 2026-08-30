"""sqlite-backed check history + reliability/quality scoring per proxy"""
import sqlite3
from datetime import datetime, timezone
from pathlib import Path

RECENT_WINDOW = 168        # last ~1 week of hourly checks
RECENT_WEIGHT = 0.7        # blend: 0.7*recent + 0.3*all-time
RT_GOOD_MS = 500           # quality 1.0 at or below this
RT_BAD_MS = 8000           # quality speed factor 0.0 at or above this
NEW_PROXY_SCORE = 0.5      # neutral score for proxies with no history

SKIP_FAIL_STREAK = 24      # consecutive fails before we stop probing every run
RECHECK_EVERY_HOURS = 24   # a skipped proxy still gets re-checked this often

TS_FMT = "%Y-%m-%dT%H:%M:%SZ"

_SCHEMA = """
CREATE TABLE IF NOT EXISTS checks (
    ip    TEXT NOT NULL,
    port  INTEGER NOT NULL,
    ts    TEXT NOT NULL,
    alive INTEGER NOT NULL,
    rt_ms INTEGER
);
CREATE INDEX IF NOT EXISTS idx_checks_proxy ON checks(ip, port, ts);
CREATE TABLE IF NOT EXISTS proxy_state (
    ip              TEXT NOT NULL,
    port            INTEGER NOT NULL,
    fails_streak    INTEGER NOT NULL DEFAULT 0,
    last_ok_ts      TEXT,
    last_checked_ts TEXT,
    PRIMARY KEY (ip, port)
) WITHOUT ROWID;
"""


class History:
    def __init__(self, path):
        self.db = sqlite3.connect(Path(path))
        self.db.executescript(_SCHEMA)
        self.db.execute("PRAGMA journal_mode=WAL")

    def record(self, outcomes, ts):
        """outcomes: iterable of (ip, port, alive: bool, rt_ms|None)"""
        self.db.executemany(
            "INSERT INTO checks VALUES (?,?,?,?,?)",
            ((ip, port, ts, int(alive), rt) for ip, port, alive, rt in outcomes),
        )
        self.db.commit()

    def state_map(self):
        """{(ip, port): {fails_streak, last_ok_ts, last_checked_ts}}"""
        rows = self.db.execute(
            "SELECT ip, port, fails_streak, last_ok_ts, last_checked_ts"
            " FROM proxy_state"
        ).fetchall()
        return {(ip, port): {"fails_streak": fs, "last_ok_ts": ok,
                             "last_checked_ts": ck}
                for ip, port, fs, ok, ck in rows}

    def update_state(self, outcomes, ts, prev):
        """bump streaks/last-seen per proxy from this run's outcomes.
        prev: state_map() loaded before this batch was checked"""
        rows = []
        for ip, port, alive, _rt in outcomes:
            st = prev.get((ip, port), {})
            streak = 0 if alive else st.get("fails_streak", 0) + 1
            last_ok = ts if alive else st.get("last_ok_ts")
            rows.append((ip, port, streak, last_ok, ts))
        self.db.executemany(
            "INSERT INTO proxy_state (ip, port, fails_streak, last_ok_ts,"
            " last_checked_ts) VALUES (?,?,?,?,?)"
            " ON CONFLICT(ip, port) DO UPDATE SET"
            " fails_streak=excluded.fails_streak,"
            " last_ok_ts=excluded.last_ok_ts,"
            " last_checked_ts=excluded.last_checked_ts",
            rows,
        )
        self.db.commit()

    def scores(self):
        """{(ip, port): {reliability, quality, checks_total, checks_ok,
        first_seen, last_seen}}"""
        rows = self.db.execute(
            "SELECT ip, port, alive, rt_ms, ts FROM checks ORDER BY ts"
        ).fetchall()

        per = {}
        for ip, port, alive, rt, ts in rows:
            key = (ip, port)
            e = per.get(key)
            if e is None:
                e = per[key] = {
                    "ok": 0, "total": 0,
                    "recent": [],           # (alive, rt) last RECENT_WINDOW
                    "first": ts, "last": ts,
                }
            e["total"] += 1
            e["ok"] += alive
            e["recent"].append((alive, rt))
            if len(e["recent"]) > RECENT_WINDOW:
                del e["recent"][0:len(e["recent"]) - RECENT_WINDOW]
            e["last"] = ts

        out = {}
        for (ip, port), e in per.items():
            total, ok = e["total"], e["ok"]
            rel_all = ok / total

            recent = e["recent"]
            n = len(recent)
            # linear recency weights: newest check weighs n, oldest 1
            wsum = n * (n + 1) / 2
            wok = sum((i + 1) * a for i, (a, _) in enumerate(recent))
            rel_recent = wok / wsum

            rel = RECENT_WEIGHT * rel_recent + (1 - RECENT_WEIGHT) * rel_all

            # speed factor from median rt of successful recent checks
            rts = sorted(rt for a, rt in recent if a and rt is not None)
            if rts:
                med = rts[len(rts) // 2]
                speed = max(0.0, min(1.0,
                            1.0 - (med - RT_GOOD_MS) / (RT_BAD_MS - RT_GOOD_MS)))
            else:
                speed = NEW_PROXY_SCORE
            quality = rel * speed

            out[(ip, port)] = {
                "reliability": round(rel, 4),
                "quality": round(quality, 4),
                "checks_total": total,
                "checks_ok": ok,
                "first_seen": e["first"],
                "last_seen": e["last"],
            }
        return out

    def close(self):
        self.db.close()


def parse_ts(s):
    return datetime.strptime(s, TS_FMT).replace(tzinfo=timezone.utc)


def skip_keys(records, state, now_ts=None):
    """(ip, port) of proxies to skip this run: fail streak reached and recently
    checked. skipped ones become eligible again after RECHECK_EVERY_HOURS"""
    now_ts = now_ts or datetime.now(timezone.utc).strftime(TS_FMT)
    now = parse_ts(now_ts)
    out = set()
    for r in records:
        st = state.get((r["ip"], r["port"]))
        if not st or st["fails_streak"] < SKIP_FAIL_STREAK:
            continue
        last = st["last_checked_ts"]
        if not last:
            continue
        try:
            hours = (now - parse_ts(last)).total_seconds() / 3600
        except ValueError:
            continue  # unparseable ts: check it, don't guess
        if hours < RECHECK_EVERY_HOURS:
            out.add((r["ip"], r["port"]))
    return out
