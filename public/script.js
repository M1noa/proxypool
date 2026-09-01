// live url builder + copy + random theme. page works without any of this.
document.addEventListener('DOMContentLoaded', () => {
    // --- random theme (pink / white / pastel), same as crypto.minoa.cat ---
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

    // --- notifications: dedupe repeats into a stacking counter instead of piling up toasts ---
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

    // --- live stat badges (total proxies, avg response, last check) ---
    const badges = document.getElementById('badges');
    if (badges) {
        function shieldsUrl(label, message, color) {
            const esc = (s) => encodeURIComponent(String(s).replace(/-/g, '--'));
            return `https://img.shields.io/badge/${esc(label)}-${esc(message)}-${color}`;
        }

        fetch('/stats')
            .then((r) => r.json())
            .then((stats) => {
                const lastCheck = stats.last_check ? stats.last_check.slice(0, 10) : 'unknown';
                const items = [
                    ['total proxies', String(stats.total), 'brightgreen'],
                    ['avg response', `${stats.avg_response_ms}ms`, 'blue'],
                    ['last check', lastCheck, 'green'],
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

    // --- live url building, collapsed to the shortest equivalent route ---
    const form = document.getElementById('generator');
    const preview = document.getElementById('url-preview');
    const copyBtn = document.getElementById('copy-btn');

    // docs / 404 share this script but have no generator form
    if (!form || !preview || !copyBtn) return;

    // aliases the worker accepts for shortcut routes -- pick the shortest
    const ALIASES = {
        anonymous: 'anon',
        education_research: 'edu',
        government_admin: 'gov',
    };

    // fields that must all be unset for a shortcut route to apply
    const REST_FIELDS = [
        'order', 'https', 'country', 'port', 'port_min', 'port_max', 'asn',
        'as_org', 'ip_version', 'source', 'min_reliability', 'min_quality',
        'response_min', 'response_max', 'first_seen_after', 'last_seen_after',
    ];

    function buildUrl() {
        const origin = window.location.origin;
        const data = new FormData(form);
        const get = (key) => (data.get(key) || '').toString().trim();
        const getAll = (key) => data.getAll(key).map(String).map((s) => s.trim()).filter(Boolean);

        // picking every option in a dimension filters nothing -- drop it, shorter url, same result
        const collapseFull = (values, name) => {
            const total = form.querySelectorAll(`input[name="${name}"]`).length;
            return values.length === total ? [] : values;
        };

        const types = collapseFull(getAll('type'), 'type');
        const anonymity = collapseFull(getAll('anonymity'), 'anonymity');
        const ipTypes = collapseFull(getAll('ip_type'), 'ip_type');
        const format = get('format') || 'json';

        const sortDefault = get('sort') === '' || get('sort') === 'response';
        const limitDefault = get('limit') === '' || get('limit') === '0';
        const restDefault = sortDefault && limitDefault && REST_FIELDS.every((f) => get(f) === '');

        if (restDefault) {
            const dims = [types.length > 0, anonymity.length > 0, ipTypes.length > 0].filter(Boolean).length;
            if (dims === 0) {
                return format === 'json' ? `${origin}/list` : `${origin}/all.${format}`;
            }
            if (dims === 1) {
                if (types.length === 1) return `${origin}/${types[0]}.${format}`;
                if (anonymity.length === 1) return `${origin}/${ALIASES[anonymity[0]] || anonymity[0]}.${format}`;
                if (ipTypes.length === 1) return `${origin}/${ALIASES[ipTypes[0]] || ipTypes[0]}.${format}`;
            }
        }

        const params = new URLSearchParams();
        for (const [key, value] of data.entries()) {
            if (key === 'type' || key === 'anonymity' || key === 'ip_type') continue;
            const v = String(value).trim();
            if (v !== '') params.append(key, v);
        }
        for (const t of types) params.append('type', t);
        for (const a of anonymity) params.append('anonymity', a);
        for (const it of ipTypes) params.append('ip_type', it);
        return `${origin}/list?${params.toString()}`;
    }

    function refresh() {
        preview.textContent = buildUrl();
    }

    form.addEventListener('input', refresh);
    form.addEventListener('change', refresh);
    refresh();

    // presets configure the form instead of navigating, with js on
    document.querySelectorAll('.presets a[data-preset]').forEach((a) => {
        a.addEventListener('click', (e) => {
            e.preventDefault();
            const { dim, value, format } = a.dataset;

            if (dim === 'all') {
                form.querySelectorAll('input[name="type"], input[name="anonymity"], input[name="ip_type"]')
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

    // js on: navigate straight to the (possibly shortcut) url instead of a
    // plain GET submit, so /socks4.txt-style collapsing actually gets used
    form.addEventListener('submit', (e) => {
        e.preventDefault();
        window.location.href = buildUrl();
    });

    copyBtn.addEventListener('click', async () => {
        const url = buildUrl();
        try {
            await navigator.clipboard.writeText(url);
        } catch {
            // clipboard api can fail on insecure contexts / permissions
            const ta = document.createElement('textarea');
            ta.value = url;
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            ta.remove();
        }
        copyBtn.classList.remove('copy-success');
        void copyBtn.offsetWidth; // restart animation
        copyBtn.classList.add('copy-success');
        notify('url copied to clipboard');
    });
});
