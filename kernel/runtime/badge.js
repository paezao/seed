// The owner's badge on my organism's pages (see badge.go): a way back to my
// control plane, and "point and ask": click anything on the page and say
// what should change. It never sends anything itself: it opens my control
// plane with the request filled in (in the URL fragment, never sent to a
// server), where my owner confirms it. Organism scripts share this page, so
// anything they could do here must need my owner's click in the control plane.
(() => {
  if (window.top !== window || document.getElementById('seed-badge')) return;
  const css = (el, s) => Object.assign(el.style, s);
  const el = (tag, style, text) => {
    const e = document.createElement(tag);
    if (style) css(e, style);
    if (text) e.textContent = text;
    return e;
  };
  const INK = '#e8ece6', MUTED = '#9aa49a', PANEL = '#10140f', GREEN = '#7cc495', LINE = 'rgba(124,196,149,.4)';
  const FONT = '13px/1.4 system-ui, -apple-system, Segoe UI, sans-serif';

  fetch('/_seed/badge', { credentials: 'same-origin', cache: 'no-store' }).then((r) => (r.status === 200 ? r.json() : null)).then((me) => {
    if (!me || document.getElementById('seed-badge')) return;
    const host = el('div', { position: 'fixed', right: '16px', bottom: '16px', zIndex: '2147483647' });
    host.id = 'seed-badge';
    const root = host.attachShadow({ mode: 'closed' });
    const ours = (e) => e.composedPath().includes(host);

    // ---- the badge
    const btn = el('button', {
      display: 'block', width: '34px', height: '34px', padding: '0', border: '0', background: 'none', cursor: 'pointer',
      color: '#5fbf85', borderRadius: '6px', outlineOffset: '4px', opacity: '.9', transition: 'opacity .2s',
      filter: 'drop-shadow(0 1px 1.5px rgba(0,0,0,.45)) drop-shadow(0 0 6px rgba(0,0,0,.18))',
    });
    btn.type = 'button';
    btn.setAttribute('aria-label', 'Seed: change something on this page, or open the control plane');
    btn.setAttribute('aria-haspopup', 'menu');
    btn.setAttribute('aria-expanded', 'false');
    const NS = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('width', '34'); svg.setAttribute('height', '34'); svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    css(svg, { display: 'block', transformOrigin: '50% 90%' });
    for (const [d, attrs] of [
      ['M12 21v-9', { stroke: 'currentColor', 'stroke-width': '1.8', 'stroke-linecap': 'round', fill: 'none' }],
      ['M12 13c0-4.4 2.9-7.3 7.8-7.3 0 4.4-2.9 7.3-7.8 7.3z', { fill: 'currentColor' }],
      ['M12 15.5c0-3.3-2.2-5.6-5.8-5.6 0 3.3 2.2 5.6 5.8 5.6z', { fill: 'currentColor', opacity: '.6' }],
    ]) {
      const p = document.createElementNS(NS, 'path');
      p.setAttribute('d', d);
      for (const [k, v] of Object.entries(attrs)) p.setAttribute(k, v);
      svg.appendChild(p);
    }
    // Who I am now: my logo (an image can't run script), or the seed.
    let face = svg;
    if (typeof me.logo === 'string' && me.logo.startsWith('/_seed/api/identity/logo')) {
      const img = el('img', { display: 'block', width: '34px', height: '34px', borderRadius: '8px', transformOrigin: '50% 90%' });
      img.src = me.logo;
      img.alt = '';
      img.width = 34; img.height = 34;
      img.addEventListener('error', () => { img.replaceWith(svg); face = svg; });
      face = img;
    }
    btn.appendChild(face);
    const still = window.matchMedia('(prefers-reduced-motion: reduce)');
    let swaying = null;
    const sway = () => {
      btn.style.opacity = '1';
      if (still.matches || !face.animate || swaying) return;
      // A sprout in a breeze: lean, sway back, settle.
      swaying = face.animate([
        { transform: 'rotate(0deg) scale(1)' },
        { transform: 'rotate(-14deg) scale(1.12)', offset: 0.25 },
        { transform: 'rotate(10deg) scale(1.12)', offset: 0.5 },
        { transform: 'rotate(-5deg) scale(1.1)', offset: 0.75 },
        { transform: 'rotate(0deg) scale(1.1)' },
      ], { duration: 900, easing: 'ease-in-out', fill: 'forwards' });
      swaying.onfinish = () => { swaying = null; };
    };
    const rest = () => {
      btn.style.opacity = '.9';
      if (!still.matches && face.animate) face.animate([{ transform: 'scale(1.1)' }, { transform: 'scale(1)' }], { duration: 200, fill: 'forwards' });
    };
    btn.addEventListener('mouseenter', sway);
    btn.addEventListener('focus', sway);
    btn.addEventListener('mouseleave', rest);
    btn.addEventListener('blur', rest);

    // ---- the menu
    const panelStyle = { background: PANEL, color: INK, border: '1px solid ' + LINE, borderRadius: '12px', boxShadow: '0 10px 30px rgba(0,0,0,.35)', font: FONT };
    const menu = el('div', Object.assign({ position: 'absolute', right: '0', bottom: '46px', minWidth: '240px', padding: '6px', display: 'none' }, panelStyle));
    menu.setAttribute('role', 'menu');
    const item = (title, sub, onPick) => {
      const b = el('button', { display: 'block', width: '100%', textAlign: 'left', padding: '8px 10px', border: '0', borderRadius: '8px', background: 'none', color: INK, font: FONT, cursor: 'pointer' });
      b.type = 'button';
      b.setAttribute('role', 'menuitem');
      b.appendChild(el('div', { fontWeight: '600' }, title));
      b.appendChild(el('div', { color: MUTED, fontSize: '12px' }, sub));
      b.addEventListener('mouseenter', () => { b.style.background = 'rgba(124,196,149,.12)'; });
      b.addEventListener('mouseleave', () => { b.style.background = 'none'; });
      b.addEventListener('click', onPick);
      return b;
    };
    const change = item('Change something here', 'Point at it and say what should change', () => { closeMenu(); startPointing(); });
    menu.append(change, item('Open my control plane', 'Chat, evolutions, settings', () => { window.location.href = '/_seed/'; }));
    const openMenu = () => { menu.style.display = 'block'; btn.setAttribute('aria-expanded', 'true'); change.focus(); };
    const closeMenu = () => { menu.style.display = 'none'; btn.setAttribute('aria-expanded', 'false'); };
    btn.addEventListener('click', () => (menu.style.display === 'none' ? openMenu() : closeMenu()));

    // ---- pointing
    const box = el('div', { position: 'fixed', pointerEvents: 'none', border: '2px solid ' + GREEN, background: 'rgba(124,196,149,.12)', borderRadius: '4px', display: 'none', boxSizing: 'border-box' });
    const label = el('div', { position: 'fixed', pointerEvents: 'none', display: 'none', padding: '3px 7px', borderRadius: '6px', background: PANEL, color: INK, font: '12px/1.3 ui-monospace, monospace', border: '1px solid ' + LINE, maxWidth: '320px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' });
    const hint = el('div', Object.assign({ position: 'fixed', top: '14px', left: '50%', transform: 'translateX(-50%)', padding: '8px 14px', display: 'none' }, panelStyle), 'Click what you want to change · Esc to cancel');
    const ask = el('form', Object.assign({ position: 'fixed', width: '320px', maxWidth: 'calc(100vw - 24px)', padding: '12px', display: 'none', boxSizing: 'border-box' }, panelStyle));
    const askTitle = el('div', { fontWeight: '600', marginBottom: '2px' }, 'What should change here?');
    const askWhat = el('div', { color: MUTED, fontSize: '12px', marginBottom: '8px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' });
    const words = el('textarea', { width: '100%', boxSizing: 'border-box', minHeight: '72px', resize: 'vertical', padding: '8px', borderRadius: '8px', border: '1px solid ' + LINE, background: '#1a1e1a', color: INK, font: FONT });
    words.placeholder = 'e.g. make this green, and a bit bigger';
    words.maxLength = 2000;
    const row = el('div', { display: 'flex', gap: '8px', justifyContent: 'flex-end', marginTop: '8px' });
    const pill = (text, primary) => {
      const b = el('button', { padding: '6px 12px', borderRadius: '8px', font: FONT, fontWeight: '600', cursor: 'pointer',
        border: '1px solid ' + (primary ? GREEN : 'rgba(232,236,230,.3)'), background: primary ? GREEN : 'none', color: primary ? '#0d140f' : INK }, text);
      return b;
    };
    const cancel = pill('Cancel', false);
    cancel.type = 'button';
    const send = pill('Continue in my control plane', true);
    send.type = 'submit';
    row.append(cancel, send);
    ask.append(askTitle, askWhat, words, row);
    root.append(box, label, hint, ask, menu, btn);

    let pointing = false, target = null;
    const describeShort = (t) => {
      const tag = t.tagName.toLowerCase();
      const name = (t.getAttribute('aria-label') || t.getAttribute('alt') || t.getAttribute('placeholder') || t.innerText || t.textContent || '').trim().replace(/\s+/g, ' ');
      return tag + (name ? ' · ' + name.slice(0, 60) : '');
    };
    const place = (t) => {
      const r = t.getBoundingClientRect();
      css(box, { display: 'block', left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: r.height + 'px' });
      label.textContent = describeShort(t);
      css(label, { display: 'block', left: Math.max(4, r.left) + 'px', top: (r.top > 28 ? r.top - 26 : r.bottom + 4) + 'px' });
    };
    const block = (e) => {
      if (!pointing || ours(e)) return;
      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
    };
    const onMove = (e) => {
      if (!pointing || ours(e)) return;
      const t = document.elementFromPoint(e.clientX, e.clientY);
      if (!t || t === host || t === document.documentElement || t === document.body) return;
      target = t;
      place(t);
    };
    const onClick = (e) => {
      if (!pointing || ours(e)) return;
      block(e);
      if (target) choose(target);
    };
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (pointing || ask.style.display !== 'none') stopAll();
      else if (menu.style.display !== 'none') { closeMenu(); btn.focus(); }
    };
    const startPointing = () => {
      pointing = true;
      hint.style.display = 'block';
      document.documentElement.style.cursor = 'crosshair';
      for (const ev of ['pointerdown', 'pointerup', 'mousedown', 'mouseup']) document.addEventListener(ev, block, true);
      document.addEventListener('click', onClick, true);
      document.addEventListener('mousemove', onMove, true);
    };
    const stopPointing = () => {
      pointing = false;
      hint.style.display = 'none';
      document.documentElement.style.cursor = '';
      for (const ev of ['pointerdown', 'pointerup', 'mousedown', 'mouseup']) document.removeEventListener(ev, block, true);
      document.removeEventListener('click', onClick, true);
      document.removeEventListener('mousemove', onMove, true);
    };
    const stopAll = () => {
      stopPointing();
      css(box, { display: 'none' });
      css(label, { display: 'none' });
      css(ask, { display: 'none' });
      target = null;
    };
    let chosen = null;
    const choose = (t) => {
      stopPointing();
      chosen = t;
      place(t);
      askWhat.textContent = describeShort(t);
      const r = t.getBoundingClientRect();
      const below = window.innerHeight - r.bottom > 200;
      const left = Math.min(Math.max(12, r.left), window.innerWidth - 332);
      css(ask, { display: 'block', left: left + 'px', top: below ? r.bottom + 10 + 'px' : 'auto', bottom: below ? 'auto' : window.innerHeight - r.top + 10 + 'px' });
      words.value = '';
      words.focus();
    };
    cancel.addEventListener('click', stopAll);
    words.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); ask.requestSubmit(); }
    });
    ask.addEventListener('submit', (e) => {
      e.preventDefault();
      if (!chosen || !words.value.trim()) { words.focus(); return; }
      const payload = { v: 1, words: words.value.trim().slice(0, 2000), page: { path: location.pathname, title: document.title.slice(0, 120) }, element: describe(chosen) };
      const bytes = new TextEncoder().encode(JSON.stringify(payload));
      let bin = '';
      bytes.forEach((b) => { bin += String.fromCharCode(b); });
      const b64 = btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
      // The fragment is never sent to a server; my control plane reads it.
      window.location.href = '/_seed/#ask=' + b64;
    });
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('pointerdown', (e) => { if (!ours(e) && menu.style.display !== 'none') closeMenu(); }, true);

    // What my owner pointed at, for me: enough to find it in my code.
    const describe = (t) => {
      const text = (t.innerText || t.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 300);
      const attrs = {};
      for (const a of ['id', 'class', 'role', 'aria-label', 'alt', 'placeholder', 'name', 'type', 'href', 'src']) {
        const v = t.getAttribute(a);
        if (v) attrs[a] = v.slice(0, 120);
      }
      const near = (t.closest('section, article, form, li, main, header, footer, nav, aside') || document.body).querySelector('h1, h2, h3, legend');
      const r = t.getBoundingClientRect();
      return {
        tag: t.tagName.toLowerCase(),
        label: describeShort(t),
        text,
        attrs,
        selector: selectorOf(t),
        near: near ? (near.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 120) : '',
        html: t.outerHTML.replace(/\s+/g, ' ').slice(0, 1500),
        rect: { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) },
        viewport: { w: window.innerWidth, h: window.innerHeight },
      };
    };
    const selectorOf = (t) => {
      const parts = [];
      for (let n = t; n && n.nodeType === 1 && parts.length < 5 && n !== document.body; n = n.parentElement) {
        let s = n.tagName.toLowerCase();
        if (n.id) { parts.unshift(s + '#' + n.id); break; }
        const cls = [...n.classList].slice(0, 2);
        if (cls.length) s += '.' + cls.join('.');
        const same = n.parentElement ? [...n.parentElement.children].filter((c) => c.tagName === n.tagName) : [];
        if (same.length > 1) s += ':nth-of-type(' + (same.indexOf(n) + 1) + ')';
        parts.unshift(s);
      }
      return parts.join(' > ');
    };

    const media = window.matchMedia('print');
    const print = () => { host.style.display = media.matches ? 'none' : ''; };
    media.addEventListener && media.addEventListener('change', print);
    document.body.appendChild(host);
  }).catch(() => {});
})();
