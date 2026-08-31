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
            document.documentElement.setAttribute('data-theme', 'pastel');
        }
    } else {
        document.documentElement.setAttribute('data-theme', theme);
    }

    // --- notifications ---
    const container = document.getElementById('notification-container');
    function notify(message, type = 'success') {
        const el = document.createElement('div');
        el.className = `notification ${type}`;
        el.textContent = message;
        container.appendChild(el);
        setTimeout(() => {
            el.style.transition = 'opacity 0.3s';
            el.style.opacity = '0';
            setTimeout(() => el.remove(), 350);
        }, 2600);
    }

    // --- live url building ---
    const form = document.getElementById('generator');
    const preview = document.getElementById('url-preview');
    const copyBtn = document.getElementById('copy-btn');

    function buildUrl() {
        const params = new URLSearchParams();
        const data = new FormData(form);
        for (const [key, value] of data.entries()) {
            const v = String(value).trim();
            if (v !== '') params.append(key, v);
        }
        return `${window.location.origin}/list?${params.toString()}`;
    }

    function refresh() {
        preview.textContent = buildUrl();
    }

    form.addEventListener('input', refresh);
    form.addEventListener('change', refresh);
    refresh();

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
