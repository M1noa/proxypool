"""fetch all sources, parse per config, save parsed sets + dedupe report.

usage: python3 tests/test_sources.py
outputs:
  test_output/parsed/<name>.json   normalized records per source
  test_output/report.txt           counts, errors, duplicate analysis
"""
import json
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from lib.parse import fetch_source
from lib.util import load_jsonc

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "test_output"
PARSED = OUT / "parsed"


def sets_of(recs):
    """ip:port -> set of protocols for overlap math"""
    out = {}
    for r in recs:
        out.setdefault(f"{r['ip']}:{r['port']}", set()).update(r["protocols"])
    return out


def main():
    cfg = load_jsonc(ROOT / "sources.jsonc")
    sources = cfg["sources"]
    PARSED.mkdir(parents=True, exist_ok=True)

    results = {}
    with ThreadPoolExecutor(max_workers=12) as pool:
        futs = {pool.submit(fetch_source, s): s["name"] for s in sources}
        for fut in as_completed(futs):
            name = futs[fut]
            recs, errors = fut.result()
            results[name] = recs
            (PARSED / f"{name}.json").write_text(json.dumps(recs))
            print(f"{name:24} {len(recs):6} proxies" + ("  ERRORS: " + "; ".join(errors) if errors else ""))

    # ---- dedupe analysis -------------------------------------------------
    lines = ["", "=" * 70, "DUPLICATE ANALYSIS", "=" * 70]
    names = sorted(results)
    sets = {n: sets_of(results[n]) for n in names}

    # exact-duplicate pairs (same keyset)
    keys = {n: frozenset(s) for n, s in sets.items()}
    exact_pairs = []
    for i, a in enumerate(names):
        for b in names[i + 1:]:
            if not keys[a] or not keys[b]:
                continue
            inter = len(keys[a] & keys[b])
            union = len(keys[a] | keys[b])
            jac = inter / union if union else 0.0
            sub_a = inter / len(keys[a])  # fraction of a contained in b
            sub_b = inter / len(keys[b])
            if jac > 0.5 or sub_a > 0.8 or sub_b > 0.8:
                exact_pairs.append((a, b, inter, jac, sub_a, sub_b))

    if exact_pairs:
        lines.append(f"\n{'source A':24} {'source B':24} {'shared':>7} {'jaccard':>8} {'a-in-b':>7} {'b-in-a':>7}")
        for a, b, inter, jac, sa, sb in sorted(exact_pairs, key=lambda x: -x[3]):
            flag = ""
            if keys[a] <= keys[b]:
                flag += f"  << {a} is SUBSET of {b}"
            elif keys[b] <= keys[a]:
                flag += f"  << {b} is SUBSET of {a}"
            elif jac > 0.95:
                flag += "  << NEAR-IDENTICAL"
            lines.append(f"{a:24} {b:24} {inter:>7} {jac:>8.2%} {sa:>7.1%} {sb:>7.1%}{flag}")
    else:
        lines.append("no significant overlaps found")

    report = "\n".join(lines)
    print(report)
    (OUT / "report.txt").write_text(
        "\n".join(f"{n}: {len(results[n])}" for n in names) + report + "\n")


if __name__ == "__main__":
    main()
