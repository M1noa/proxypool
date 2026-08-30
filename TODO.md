# todo

- [x] 1. workflow: rebase -X ours + push HEAD:main, drop history.db line
- [x] 2. lib: {path,map} extract fields, pages_path, urls+set entries, 12s default timeout, 80s source budget
- [x] 3. sources.jsonc: migrate iproyal/proxyshare/myproxy, add goodips html, comment out spysone, trim flows.py
- [x] 4. fetch: parallel pools (github x3), 429 requeue, >8s watchdog, per-source/per-phase timing logs
- [x] 5. check: cumulative eta, shuffle, mt speedtest -> concurrency = clamp(mbps*2, 128, 4096)
- [x] 6. housekeeping: rm AGENTS.md, README notice, +psutil
- [x] 7. verify: migrated sources match old flow counts; full --no-check run (4.0m recs, 150 sources, 348s); checker smoke

## later
- [ ] go rewrite (this python code is the frozen spec)
- [ ] proxynova flow fix (user)
- [ ] sixsixdaili api timing out from this network — watch it
