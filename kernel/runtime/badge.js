// The owner's badge on my organism's pages (see badge.go): a way back to my
// control plane, and "point and ask": click anything on the page and say
// what should change. It never sends a message itself: it opens my control
// plane with the request filled in (in the URL fragment, never sent to a
// server), where my owner confirms it. A screenshot goes ahead as a draft,
// which does nothing until my owner sends it (see ask.go). Organism scripts
// share this page, so anything they could do here must need my owner's
// click in the control plane.
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
    btn.setAttribute('aria-label', 'Open my control plane');
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

    // ---- the wheel: hovering the seed fans out what it can do, so one
    // click does it. Clicking the seed itself opens my control plane.
    const panelStyle = { background: PANEL, color: INK, border: '1px solid ' + LINE, borderRadius: '12px', boxShadow: '0 10px 30px rgba(0,0,0,.35)', font: FONT };
    // A quarter disc behind the options, so the pointer can travel from the
    // seed to an option without the wheel closing.
    const menu = el('div', { position: 'absolute', right: '-10px', bottom: '-10px', width: '150px', height: '150px', borderTopLeftRadius: '100%', pointerEvents: 'none' });
    const icon = (paths) => {
      const g = document.createElementNS(NS, 'svg');
      g.setAttribute('width', '18'); g.setAttribute('height', '18'); g.setAttribute('viewBox', '0 0 24 24');
      g.setAttribute('aria-hidden', 'true');
      g.setAttribute('fill', 'none'); g.setAttribute('stroke', 'currentColor'); g.setAttribute('stroke-width', '2');
      g.setAttribute('stroke-linecap', 'round'); g.setAttribute('stroke-linejoin', 'round');
      for (const d of paths) {
        const p = document.createElementNS(NS, 'path');
        p.setAttribute('d', d);
        g.appendChild(p);
      }
      return g;
    };
    const options = [];
    // Angles (degrees, counter-clockwise from the right): up-left of the
    // seed, since it sits in the bottom-right corner.
    const option = (name, paths, angle, onPick) => {
      const a = (angle * Math.PI) / 180, R = 62;
      const x = Math.round(Math.cos(a) * R), y = Math.round(-Math.sin(a) * R);
      const b = el('button', {
        position: 'absolute', left: (x - 2) + 'px', top: (y - 2) + 'px', width: '38px', height: '38px', padding: '0',
        display: 'grid', placeItems: 'center', borderRadius: '50%', cursor: 'pointer', color: GREEN, background: PANEL,
        border: '1px solid ' + LINE, boxShadow: '0 6px 18px rgba(0,0,0,.35)', outlineOffset: '3px',
        opacity: '0', transform: 'translate(' + (-x) + 'px,' + (-y) + 'px) scale(.4)', pointerEvents: 'none',
        transition: 'transform .22s cubic-bezier(.2,.9,.3,1.3), opacity .15s, background .15s',
      });
      b.type = 'button';
      b.tabIndex = -1;
      b.setAttribute('aria-label', name);
      b.appendChild(icon(paths));
      // Its name, to its left.
      const tip = el('span', {
        position: 'absolute', right: '46px', top: '50%', transform: 'translateY(-50%)', whiteSpace: 'nowrap',
        padding: '4px 9px', borderRadius: '8px', background: PANEL, color: INK, border: '1px solid ' + LINE,
        font: '12px/1.3 system-ui, -apple-system, Segoe UI, sans-serif', fontWeight: '600', pointerEvents: 'none',
        boxShadow: '0 4px 14px rgba(0,0,0,.3)', opacity: '.85', transition: 'opacity .15s',
      }, name);
      tip.setAttribute('aria-hidden', 'true');
      b.appendChild(tip);
      const lit = (on) => { b.style.background = on ? '#1c2a20' : PANEL; tip.style.opacity = on ? '1' : '.85'; };
      b.addEventListener('mouseenter', () => lit(true));
      b.addEventListener('mouseleave', () => lit(false));
      b.addEventListener('focus', () => lit(true));
      b.addEventListener('blur', () => lit(false));
      b.addEventListener('click', (e) => { e.stopPropagation(); closeMenu(); onPick(); });
      options.push({ b, x, y });
      return b;
    };
    const POINT = ['M3 3l7.07 16.97 2.51-7.39 7.39-2.51L3 3z', 'M13 13l6 6'];
    const PANEL_ICON = ['M4 4h16v16H4z', 'M9 4v16', 'M13 9h4', 'M13 13h4'];
    option('Change something here', POINT, 170, () => startPointing());
    option('Open my control plane', PANEL_ICON, 100, () => { window.location.href = '/_seed/'; });
    let open = false, closing = null;
    const isOpen = () => open;
    const openMenu = () => {
      clearTimeout(closing);
      if (open || pointing || ask.style.display !== 'none') return;
      open = true;
      menu.style.pointerEvents = 'auto';
      options.forEach((o, i) => {
        o.b.style.transitionDelay = (i * 40) + 'ms';
        Object.assign(o.b.style, { opacity: '1', transform: 'translate(0,0) scale(1)', pointerEvents: 'auto' });
        o.b.tabIndex = 0;
      });
    };
    const closeMenu = () => {
      clearTimeout(closing);
      if (!open) return;
      open = false;
      menu.style.pointerEvents = 'none';
      options.forEach((o) => {
        o.b.style.transitionDelay = '0ms';
        Object.assign(o.b.style, { opacity: '0', transform: 'translate(' + (-o.x) + 'px,' + (-o.y) + 'px) scale(.4)', pointerEvents: 'none' });
        o.b.tabIndex = -1;
      });
    };
    const closeSoon = () => { clearTimeout(closing); closing = setTimeout(closeMenu, 280); };
    host.addEventListener('mouseenter', openMenu);
    host.addEventListener('mouseleave', closeSoon);
    host.addEventListener('focusin', openMenu);
    host.addEventListener('focusout', (e) => { if (!host.contains(e.relatedTarget) && !root.contains(e.relatedTarget)) closeSoon(); });
    // Touch has no hover: the first tap fans the wheel out, the next one on
    // the seed opens my control plane.
    let lastPointer = 'mouse';
    btn.addEventListener('pointerdown', (e) => { lastPointer = e.pointerType || 'mouse'; });
    btn.addEventListener('click', () => {
      if (lastPointer !== 'mouse' && !open) { openMenu(); return; }
      window.location.href = '/_seed/';
    });

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
    root.append(box, label, hint, ask, menu, ...options.map((o) => o.b), btn);

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
      else if (isOpen()) { closeMenu(); btn.focus(); }
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
    let sending = false;
    ask.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (sending) return;
      if (!chosen || !words.value.trim()) { words.focus(); return; }
      sending = true;
      send.disabled = true;
      send.textContent = 'Taking a picture…';
      const payload = { v: 1, words: words.value.trim().slice(0, 2000), page: { path: location.pathname, title: document.title.slice(0, 120) }, element: describe(chosen) };
      // A picture of the page helps my mind see what my owner sees. It is
      // left as a draft that does nothing until my owner sends it.
      const r = chosen.getBoundingClientRect();
      css(ask, { display: 'none' });
      css(box, { display: 'none' });
      css(label, { display: 'none' });
      const shot = await screenshot(r).catch(() => null);
      if (shot) payload.shot = shot;
      const bytes = new TextEncoder().encode(JSON.stringify(payload));
      let bin = '';
      bytes.forEach((b) => { bin += String.fromCharCode(b); });
      const b64 = btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
      // The fragment is never sent to a server; my control plane reads it.
      window.location.href = '/_seed/#ask=' + b64;
    });

    // screenshot draws the visible page (without me), outlines what was
    // pointed at, and leaves it as a draft; it resolves to the draft's id.
    const loadDrawer = () => new Promise((resolve, reject) => {
      if (window.modernScreenshot) { resolve(window.modernScreenshot); return; }
      const s = document.createElement('script');
      s.src = '/_seed/screenshot.js?v=4.7.0';
      s.onload = () => (window.modernScreenshot ? resolve(window.modernScreenshot) : reject(new Error('no drawer')));
      s.onerror = reject;
      document.head.appendChild(s);
    });
    const within = (ms, p) => Promise.race([p, new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), ms))]);
    const screenshot = (r) => within(10000, (async () => {
      const ms = await loadDrawer();
      const vw = window.innerWidth, vh = window.innerHeight, sx = window.scrollX, sy = window.scrollY;
      const root = document.documentElement;
      const full = await ms.domToCanvas(root, {
        filter: (n) => n !== host,
        width: Math.max(root.scrollWidth, vw),
        height: Math.min(root.scrollHeight, sy + vh),
        scale: 1,
        backgroundColor: getComputedStyle(document.body).backgroundColor || getComputedStyle(root).backgroundColor || '#fff',
      });
      // What my owner saw around what they pointed at: a window-sized
      // region (at most 1100×750) centred on it, so it isn't lost in a
      // wide, empty page.
      const cw = Math.min(vw, Math.max(1100, r.width + 80)), ch = Math.min(vh, Math.max(750, r.height + 80));
      const cx = Math.min(Math.max(0, r.left + r.width / 2 - cw / 2), vw - cw);
      const cy = Math.min(Math.max(0, r.top + r.height / 2 - ch / 2), vh - ch);
      const k = Math.min(1, 1280 / cw);
      const out = document.createElement('canvas');
      out.width = Math.round(cw * k);
      out.height = Math.round(ch * k);
      const g = out.getContext('2d');
      g.drawImage(full, sx + cx, sy + cy, cw, ch, 0, 0, out.width, out.height);
      g.strokeStyle = GREEN;
      g.lineWidth = 3;
      g.strokeRect((r.left - cx) * k - 3, (r.top - cy) * k - 3, r.width * k + 6, r.height * k + 6);
      const blob = await new Promise((resolve) => out.toBlob(resolve, 'image/jpeg', 0.82));
      if (!blob) throw new Error('no image');
      const res = await fetch('/_seed/ask-draft', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'image/jpeg' }, body: blob });
      if (res.status !== 201) throw new Error('not kept');
      const d = await res.json();
      return typeof d.id === 'string' ? d.id : null;
    })());
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('pointerdown', (e) => { if (!ours(e) && isOpen()) closeMenu(); }, true);

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
