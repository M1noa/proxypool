// agents page: live url builder + copy + random theme + live match stats.
// mirrors script.js for the /uas api. the page works without any of this.
document.addEventListener('DOMContentLoaded', () => {
    // --- random theme (pink / white / pastel), same as the proxies page ---
    function randomPastel() {
        const hue = Math.floor(Math.random() * 360);
        return `hsl(${hue}, ${30 + Math.floor(Math.random() * 40)}%, ${70 + Math.floor(Math.random() * 20)}%)`;
    }

    const theme = ['pink', 'white', 'pastel'][Math.floor(Math.random() * 3)];
    if (theme === 'pastel') {
        const tempDiv = document.createElement('div');
        tempDiv.style.color = randomPastel();
        document.body.appendChild(tempDiv);
        const rgb = window.getComputedStyle(tempDiv).color.match(/rgb\((\d+),\s*(\d+),\s*(\d+)\)/);
        document.body.removeChild(tempDiv);
        if (rgb) {
            const [, r, g, b] = rgb;
            const root = document.documentElement;
            const set = (k, v) => root.style.setProperty(k, v);
            set('--text', `rgba(${r}, ${g}, ${b}, 0.85)`);
            set('--text-muted', `rgba(${r}, ${g}, ${b}, 0.5)`);
            set('--text-bright', `rgba(${r}, ${g}, ${b}, 0.95)`);
            set('--card-bg', `rgba(${r}, ${g}, ${b}, 0.1)`);
            set('--card-hover', `rgba(${r}, ${g}, ${b}, 0.15)`);
            set('--border', `rgba(${r}, ${g}, ${b}, 0.2)`);
            set('--hover-border', `rgba(${r}, ${g}, ${b}, 0.4)`);
            set('--button-bg', `rgba(${r}, ${g}, ${b}, 0.15)`);
            set('--button-hover', `rgba(${r}, ${g}, ${b}, 0.25)`);
            set('--button-shadow-soft', `rgba(${r}, ${g}, ${b}, 0.12)`);
            set('--button-shadow-strong', `rgba(${r}, ${g}, ${b}, 0.18)`);
            set('--link-color', `rgb(${r}, ${g}, ${b})`);
            set('--link-hover', `rgba(${r}, ${g}, ${b}, 0.8)`);
            set('--notification-info-bg', `rgba(${r}, ${g}, ${b}, 0.14)`);
            document.documentElement.setAttribute('data-theme', 'pastel');
        }
    } else {
        document.documentElement.setAttribute('data-theme', theme);
    }

    // --- notifications ---
    const container = document.getElementById('notification-container');
    const activeNotifications = new Map();

    function hideNotification(key) {
        const entry = activeNotifications.get(key);
        if (!entry) return;
        entry.element.classList.remove('is-refreshing');
        entry.element.style.opacity = '0';
        entry.element.style.transform = 'translateX(120%)';
        clearTimeout(entry.hideTimeout);
        clearTimeout(entry.refreshTimeout);
        setTimeout(() => entry.element.remove(), 380);
        activeNotifications.delete(key);
    }

    function notify(message, type = 'success') {
        if (!container) return;
        const key = `${type}:${message}`;
        const existing = activeNotifications.get(key);
        if (existing) {
            existing.count += 1;
            existing.countNode.textContent = `×${existing.count}`;
            existing.element.classList.add('is-stacked', 'is-refreshing');
            clearTimeout(existing.hideTimeout);
            clearTimeout(existing.refreshTimeout);
            existing.refreshTimeout = setTimeout(() => existing.element.classList.remove('is-refreshing'), 180);
            existing.hideTimeout = setTimeout(() => hideNotification(key), 2600);
            return;
        }
        const el = document.createElement('div');
        el.className = `notification ${type}`;
        const messageNode = document.createElement('span');
        messageNode.className = 'notification__message';
        messageNode.textContent = message;
        const countNode = document.createElement('span');
        countNode.className = 'notification__count';
        countNode.textContent = '×1';
        el.append(messageNode, countNode);
        container.appendChild(el);
        const entry = { element: el, count: 1, countNode, hideTimeout: null, refreshTimeout: null };
        activeNotifications.set(key, entry);
        entry.hideTimeout = setTimeout(() => hideNotification(key), 2600);
    }

    // --- live stat badges (total agents, top browser/os, generated date) ---
    const badges = document.getElementById('badges');
    if (badges) {
        function shieldsUrl(label, message, color) {
            const esc = (s) => encodeURIComponent(String(s).replace(/-/g, '--'));
            return `https://img.shields.io/badge/${esc(label)}-${esc(message)}-${color}`;
        }

        fetch('/ua-stats')
            .then((r) => r.json())
            .then((stats) => {
                const gen = stats.generated_at ? stats.generated_at.slice(0, 10) : 'unknown';
                const items = [
                    ['total agents', String(stats.total), 'brightgreen'],
                    ['top browser', String(stats.top_browser), 'blue'],
                    ['top os', String(stats.top_os), 'blue'],
                    ['generated', gen, 'green'],
                ];
                for (const [label, message, color] of items) {
                    const img = document.createElement('img');
                    img.src = shieldsUrl(label, message, color);
                    img.alt = `${label}: ${message}`;
                    img.width = 120;
                    img.height = 20;
                    badges.prepend(img);
                }
            })
            .catch(() => {});
    }

    // --- live url building ---
    const form = document.getElementById('generator');
    const preview = document.getElementById('url-preview');
    const copyBtn = document.getElementById('copy-btn');

    if (!form || !preview || !copyBtn) return;

    const DIMS = ['browser', 'os', 'device'];
    const REST_FIELDS = ['order', 'version', 'min_share', 'max_share'];

    function buildUrl() {
        const origin = window.location.origin;
        const data = new FormData(form);
        const get = (key) => (data.get(key) || '').toString().trim();
        const getAll = (key) => data.getAll(key).map(String).map((s) => s.trim()).filter(Boolean);

        const collapseFull = (values, name) => {
            const total = form.querySelectorAll(`input[name="${name}"]`).length;
            return values.length === total ? [] : values;
        };

        const picked = {};
        for (const d of DIMS) picked[d] = collapseFull(getAll(d), d);
        const format = get('format') || 'json';

        const sortDefault = get('sort') === '' || get('sort') === 'share';
        const limitDefault = get('limit') === '' || get('limit') === '0';
        const restDefault = sortDefault && limitDefault && REST_FIELDS.every((f) => get(f) === '');

        if (restDefault) {
            const dims = DIMS.filter((d) => picked[d].length > 0);
            if (dims.length === 0) {
                return format === 'json' ? `${origin}/uas` : `${origin}/ua-all.${format}`;
            }
            if (dims.length === 1 && picked[dims[0]].length === 1) {
                return `${origin}/ua-${picked[dims[0]][0]}.${format}`;
            }
        }

        const params = new URLSearchParams();
        for (const [key, value] of data.entries()) {
            if (DIMS.includes(key)) continue;
            const v = String(value).trim();
            if (v !== '') params.append(key, v);
        }
        for (const d of DIMS) for (const v of picked[d]) params.append(d, v);
        return `${origin}/uas?${params.toString()}`;
    }

    // --- live match stats: fetch the built url as json, count + average locally
    // shortcut paths pin one dim, so they expand back to /uas query form here
    // (the path would otherwise override format=json on the worker side).
    const UA_SHORTCUT_DIMS = {
        chrome: 'browser', firefox: 'browser', safari: 'browser', edge: 'browser',
        opera: 'browser', samsung: 'browser', vivaldi: 'browser', yandex: 'browser',
        ie: 'browser', windows: 'os', macos: 'os', linux: 'os', android: 'os',
        ios: 'os', chromeos: 'os', desktop: 'device', mobile: 'device', tablet: 'device',
    };

    function liveFetchUrl(url) {
        const u = new URL(url);
        const m = /^\/ua-([a-z0-9_]+)\.(txt|json|jsonl|csv)$/.exec(u.pathname);
        if (m) {
            const dim = m[1] === 'all' ? null : UA_SHORTCUT_DIMS[m[1]];
            if (m[1] !== 'all' && !dim) return null;
            const q = new URLSearchParams(u.search);
            q.set('format', 'json');
            q.delete('limit'); // probe counts every match, not the capped link
            if (dim) q.set(dim, m[1]);
            return `${u.origin}/uas?${q.toString()}`;
        }
        if (u.pathname !== '/uas') return null;
        const q = new URLSearchParams(u.search);
        q.set('format', 'json');
        q.delete('limit');
        return `${u.origin}/uas?${q.toString()}`;
    }

    function liveLimit(url) {
        const u = new URL(url, window.location.origin);
        const n = Number(u.searchParams.get('limit'));
        return Number.isFinite(n) && n > 0 ? n : 0;
    }

    const liveStats = document.getElementById('live-stats');
    const liveCount = document.getElementById('live-count');
    const liveAvg = document.getElementById('live-avg');
    const liveExamples = document.getElementById('live-examples');
    let liveTimer = null;
    let liveSeq = 0;

    function refreshLive() {
        if (!liveStats || !liveCount || !liveAvg || !liveExamples) return;
        const mySeq = ++liveSeq;
        const url = buildUrl();
        const fetchUrl = liveFetchUrl(url);
        if (!fetchUrl) return;
        fetch(fetchUrl)
            .then((r) => (r.ok ? r.json() : null))
            .then((records) => {
                if (mySeq !== liveSeq || !Array.isArray(records)) return;
                const n = records.length;
                liveStats.hidden = false;
                const cap = liveLimit(url);
                liveCount.textContent = cap > 0 && cap < n ? `${n} (link returns ${cap})` : String(n);
                if (n === 0) {
                    liveAvg.textContent = '–';
                    liveExamples.innerHTML = '';
                    return;
                }
                const avg = records.reduce((s, r) => s + (r.share || 0), 0) / n;
                liveAvg.textContent = avg.toFixed(4);
                liveExamples.innerHTML = '';
                for (const r of records.slice(0, 3)) {
                    const li = document.createElement('li');
                    const code = document.createElement('code');
                    code.textContent = r.ua.length > 110 ? r.ua.slice(0, 110) + '…' : r.ua;
                    code.title = r.ua;
                    const span = document.createElement('span');
                    span.textContent = ` — ${r.browser} ${r.browser_version}, ${r.os}, share ${Number(r.share).toFixed(4)}`;
                    li.append(code, span);
                    liveExamples.appendChild(li);
                }
            })
            .catch(() => {});
    }

    function refresh() {
        preview.textContent = buildUrl();
        if (liveTimer) clearTimeout(liveTimer);
        liveTimer = setTimeout(refreshLive, 400);
    }

    form.addEventListener('input', refresh);
    form.addEventListener('change', refresh);
    refresh();

    document.querySelectorAll('.presets a[data-preset]').forEach((a) => {
        a.addEventListener('click', (e) => {
            e.preventDefault();
            const { dim, value, format } = a.dataset;
            if (dim === 'all') {
                form.querySelectorAll('input[name="browser"], input[name="os"], input[name="device"]')
                    .forEach((el) => { el.checked = false; });
            } else if (dim) {
                form.querySelectorAll(`input[name="${dim}"]`).forEach((el) => { el.checked = false; });
                if (value) {
                    const target = form.querySelector(`input[name="${dim}"][value="${value}"]`);
                    if (target) target.checked = true;
                }
            }
            if (format) {
                const f = form.querySelector(`input[name="format"][value="${format}"]`);
                if (f) f.checked = true;
            }
            refresh();
        });
    });

    form.addEventListener('submit', (e) => {
        e.preventDefault();
        window.location.href = buildUrl();
    });

    copyBtn.addEventListener('click', async () => {
        const url = buildUrl();
        try {
            await navigator.clipboard.writeText(url);
        } catch {
            const ta = document.createElement('textarea');
            ta.value = url;
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            ta.remove();
        }
        copyBtn.classList.remove('copy-success');
        void copyBtn.offsetWidth;
        copyBtn.classList.add('copy-success');
        notify('url copied to clipboard');
    });
});
