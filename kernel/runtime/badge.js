// The owner's badge on my organism's pages (see badge.go): a way back to my
// control plane, and "point and ask": drag a box around part of the page
// and say what should change. It never sends a message itself: it opens my
// control plane with the request filled in (in the URL fragment, never sent
// to a server), where my owner confirms it. The picture of the area goes
// ahead as a draft, which does nothing until my owner sends it (see
// ask.go). Organism scripts share this page, so anything they could do here
// must need my owner's click in the control plane.
(() => {
  if (window.top !== window || document.getElementById('seed-badge')) return;
  const css = (el, s) => Object.assign(el.style, s);
  const el = (tag, style, text) => {
    const e = document.createElement(tag);
    if (style) css(e, style);
    if (text) e.textContent = text;
    return e;
  };
  // I wear my organism's colours and font, read from its page, so I look
  // like part of the app. They are CSS variables on my host, read again
  // whenever I open (the page may have switched between light and dark).
  const INK = 'var(--sb-ink)', MUTED = 'var(--sb-muted)', PANEL = 'var(--sb-panel)', GREEN = 'var(--sb-accent)', LINE = 'var(--sb-line)';
  const HOVER = 'var(--sb-hover)', SOFT = 'var(--sb-soft)', FIELD = 'var(--sb-field)', ON_ACCENT = 'var(--sb-on-accent)', SHADOW = 'var(--sb-shadow)';
  const FONT = '13px/1.4 var(--sb-font)';
  // Any CSS colour (rgb, hex, oklch, a variable's value\u2026) as [r, g, b, a].
  const probe = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
  const rgbaOf = (c) => {
    if (!c || !probe) return null;
    probe.fillStyle = 'rgba(0,0,0,0)';
    probe.fillStyle = c;
    probe.clearRect(0, 0, 1, 1);
    probe.fillRect(0, 0, 1, 1);
    const d = probe.getImageData(0, 0, 1, 1).data;
    return [d[0], d[1], d[2], d[3] / 255];
  };
  const solid = (c) => { const v = rgbaOf(c); return v && v[3] > 0.5 ? v : null; };
  const rgb = (v, a) => 'rgba(' + v[0] + ',' + v[1] + ',' + v[2] + ',' + (a == null ? 1 : a) + ')';
  const mix = (a, b, t) => [0, 1, 2].map((i) => Math.round(a[i] + (b[i] - a[i]) * t)).concat(1);
  const lum = (v) => {
    const f = (x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); };
    return 0.2126 * f(v[0]) + 0.7152 * f(v[1]) + 0.0722 * f(v[2]);
  };
  const contrast = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const vivid = (v) => { const hi = Math.max(v[0], v[1], v[2]), lo = Math.min(v[0], v[1], v[2]); return hi > 0 && (hi - lo) / hi > 0.25; };
  const bgOf = (n) => {
    for (; n; n = n.parentElement) { const v = solid(getComputedStyle(n).backgroundColor); if (v) return v; }
    return null;
  };
  // The app's accent: a variable it names as such, or its buttons, or its links.
  const accentOf = (bg) => {
    const rs = getComputedStyle(document.documentElement);
    for (const name of ['--accent', '--primary', '--color-primary', '--accent-color', '--primary-color', '--color-accent', '--brand']) {
      const v = solid(rs.getPropertyValue(name).trim());
      if (v && contrast(v, bg) > 1.5) return v;
    }
    for (const b of [...document.querySelectorAll('button, [role=button], input[type=submit], .btn, .button')].slice(0, 40)) {
      const v = solid(getComputedStyle(b).backgroundColor);
      if (v && vivid(v) && contrast(v, bg) > 1.5) return v;
    }
    for (const a of [...document.querySelectorAll('a[href]')].slice(0, 40)) {
      const v = solid(getComputedStyle(a).color);
      if (v && vivid(v) && contrast(v, bg) > 2) return v;
    }
    return null;
  };
  let accentNow = '#5fbf85';
  const theme = (host) => {
    const body = document.body;
    const bg = bgOf(body) || [255, 255, 255, 1];
    const dark = lum(bg) < 0.4;
    let ink = solid(getComputedStyle(body).color);
    if (!ink || contrast(ink, bg) < 4) ink = dark ? [236, 236, 236, 1] : [24, 24, 24, 1];
    const panel = dark ? mix(bg, ink, 0.08) : mix(bg, [255, 255, 255], 0.85);
    const accent = accentOf(bg) || [95, 191, 133, 1];
    const onAccent = contrast([255, 255, 255], accent) >= contrast([17, 17, 17], accent) ? [255, 255, 255, 1] : [17, 17, 17, 1];
    accentNow = rgb(accent);
    const vars = {
      ink: rgb(ink), muted: rgb(ink, 0.62), panel: rgb(panel), line: rgb(ink, 0.16), accent: accentNow,
      soft: rgb(accent, 0.14), hover: rgb(mix(panel, accent, 0.16)), field: rgb(mix(bg, ink, dark ? 0.03 : 0)),
      'on-accent': rgb(onAccent), shadow: dark ? 'rgba(0,0,0,.4)' : 'rgba(0,0,0,.14)',
      font: getComputedStyle(body).fontFamily || 'system-ui, sans-serif',
    };
    for (const [k, v] of Object.entries(vars)) host.style.setProperty('--sb-' + k, v);
  };

  fetch('/_seed/badge', { credentials: 'same-origin', cache: 'no-store' }).then((r) => (r.status === 200 ? r.json() : null)).then((me) => {
    if (!me || document.getElementById('seed-badge')) return;
    const host = el('div', { position: 'fixed', right: '16px', bottom: '16px', zIndex: '2147483647' });
    host.id = 'seed-badge';
    theme(host);
    const root = host.attachShadow({ mode: 'closed' });
    const ours = (e) => e.composedPath().includes(host);

    // ---- the badge
    const btn = el('button', {
      display: 'block', width: '34px', height: '34px', padding: '0', border: '0', background: 'none', cursor: 'pointer',
      color: GREEN, borderRadius: '6px', outlineOffset: '4px', opacity: '.9', transition: 'opacity .2s',
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
    const panelStyle = { background: PANEL, color: INK, border: '1px solid ' + LINE, borderRadius: '12px', boxShadow: '0 10px 30px ' + SHADOW, font: FONT };
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
      // The whole option is one button: its name and its icon, with some
      // room around them, so it doesn't take aim to hit. Its right edge
      // sits so the icon's centre is on the wheel.
      const PAD = 6;
      const b = el('button', {
        position: 'absolute', right: (-2 - x - PAD) + 'px', top: (y - 2 - PAD) + 'px', padding: PAD + 'px',
        display: 'flex', alignItems: 'center', gap: '8px', border: '0', borderRadius: '999px', background: 'none',
        cursor: 'pointer', outlineOffset: '-2px', whiteSpace: 'nowrap',
        opacity: '0', transform: 'translate(' + (-x) + 'px,' + (-y) + 'px) scale(.4)', transformOrigin: 'right center', pointerEvents: 'none',
        transition: 'transform .22s cubic-bezier(.2,.9,.3,1.3), opacity .15s',
      });
      b.type = 'button';
      b.tabIndex = -1;
      b.setAttribute('aria-label', name);
      const tip = el('span', {
        padding: '5px 10px', borderRadius: '8px', background: PANEL, color: INK, border: '1px solid ' + LINE,
        font: '12px/1.3 var(--sb-font)', fontWeight: '600', boxShadow: '0 4px 14px ' + SHADOW, opacity: '.9',
        transition: 'opacity .15s, background .15s',
      }, name);
      tip.setAttribute('aria-hidden', 'true');
      const disc = el('span', {
        display: 'grid', placeItems: 'center', width: '38px', height: '38px', boxSizing: 'border-box', borderRadius: '50%',
        color: GREEN, background: PANEL, border: '1px solid ' + LINE, boxShadow: '0 6px 18px ' + SHADOW, transition: 'background .15s',
      });
      disc.appendChild(icon(paths));
      b.append(tip, disc);
      const lit = (on) => {
        disc.style.background = on ? HOVER : PANEL;
        tip.style.background = on ? HOVER : PANEL;
        tip.style.opacity = on ? '1' : '.9';
      };
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
      theme(host);
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

    // ---- selecting an area: my owner drags a box around what should
    // change; the picture of exactly that area is what I get.
    // A sheet over the page catches the drag, so the page never sees it.
    const sheet = el('div', { position: 'fixed', inset: '0', cursor: 'crosshair', display: 'none', touchAction: 'none' });
    // The box dims everything outside it, with a ring in the panel's colour
    // so it shows on any background.
    const box = el('div', { position: 'fixed', pointerEvents: 'none', border: '2px solid ' + GREEN, borderRadius: '4px', display: 'none', boxSizing: 'border-box',
      boxShadow: '0 0 0 2px ' + PANEL + ', 0 0 0 9999px rgba(0,0,0,.28)' });
    const hint = el('div', Object.assign({ position: 'fixed', top: '14px', left: '50%', transform: 'translateX(-50%)', padding: '8px 14px', display: 'none', pointerEvents: 'none', whiteSpace: 'nowrap' }, panelStyle), 'Drag a box around what you want to change \u00b7 Esc to cancel');
    const ask = el('form', Object.assign({ position: 'fixed', width: '320px', maxWidth: 'calc(100vw - 24px)', padding: '12px', display: 'none', boxSizing: 'border-box' }, panelStyle));
    const askTitle = el('div', { fontWeight: '600', marginBottom: '8px' }, 'What should change here?');
    const words = el('textarea', { width: '100%', boxSizing: 'border-box', minHeight: '72px', resize: 'vertical', padding: '8px', borderRadius: '8px', border: '1px solid ' + LINE, background: FIELD, color: INK, font: FONT });
    words.addEventListener('focus', () => { words.style.outline = '2px solid ' + GREEN; words.style.outlineOffset = '-1px'; });
    words.addEventListener('blur', () => { words.style.outline = ''; });
    words.placeholder = 'e.g. make this green, and a bit bigger';
    words.maxLength = 2000;
    const row = el('div', { display: 'flex', gap: '8px', justifyContent: 'flex-end', marginTop: '8px' });
    const pill = (text, primary) => el('button', { padding: '6px 12px', borderRadius: '8px', font: FONT, fontWeight: '600', cursor: 'pointer',
      border: '1px solid ' + (primary ? GREEN : LINE), background: primary ? GREEN : 'none', color: primary ? ON_ACCENT : INK }, text);
    const cancel = pill('Cancel', false);
    cancel.type = 'button';
    const send = pill('Continue in my control plane', true);
    send.type = 'submit';
    row.append(cancel, send);
    ask.append(askTitle, words, row);
    root.append(sheet, box, hint, ask, menu, ...options.map((o) => o.b), btn);

    let pointing = false, start = null, area = null;
    const rectOf = (a, b) => ({ left: Math.min(a.x, b.x), top: Math.min(a.y, b.y), width: Math.abs(a.x - b.x), height: Math.abs(a.y - b.y) });
    const showBox = (r) => css(box, { display: 'block', left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: r.height + 'px' });
    sheet.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      e.preventDefault();
      start = { x: e.clientX, y: e.clientY };
      sheet.setPointerCapture(e.pointerId);
      hint.style.display = 'none';
    });
    sheet.addEventListener('pointermove', (e) => {
      if (start) showBox(rectOf(start, { x: e.clientX, y: e.clientY }));
    });
    sheet.addEventListener('pointerup', (e) => {
      if (!start) return;
      const r = rectOf(start, { x: e.clientX, y: e.clientY });
      start = null;
      if (r.width < 12 || r.height < 12) { // a click, not a box: try again
        css(box, { display: 'none' });
        hint.style.display = 'block';
        return;
      }
      choose(r);
    });
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (pointing || ask.style.display !== 'none') stopAll();
      else if (isOpen()) { closeMenu(); btn.focus(); }
    };
    const startPointing = () => {
      theme(host);
      pointing = true;
      start = null;
      css(box, { display: 'none' });
      sheet.style.display = 'block';
      hint.style.display = 'block';
    };
    const stopPointing = () => {
      pointing = false;
      start = null;
      sheet.style.display = 'none';
      hint.style.display = 'none';
    };
    const stopAll = () => {
      stopPointing();
      css(box, { display: 'none' });
      css(ask, { display: 'none' });
      area = null;
    };
    const choose = (r) => {
      stopPointing();
      area = r;
      showBox(r);
      const below = window.innerHeight - (r.top + r.height) > 200;
      const left = Math.min(Math.max(12, r.left), window.innerWidth - 332);
      css(ask, { display: 'block', left: left + 'px', top: below ? r.top + r.height + 10 + 'px' : 'auto', bottom: below ? 'auto' : Math.min(window.innerHeight - 200, window.innerHeight - r.top + 10) + 'px' });
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
      if (!area || !words.value.trim()) { words.focus(); return; }
      sending = true;
      send.disabled = true;
      send.textContent = 'Taking a picture\u2026';
      const payload = { v: 2, words: words.value.trim().slice(0, 2000), page: { path: location.pathname } };
      // The picture is left as a draft that does nothing until my owner sends it.
      const r = area;
      css(ask, { display: 'none' });
      css(box, { display: 'none' });
      const shot = await screenshot(r).catch(() => null);
      if (shot) payload.shot = shot;
      const bytes = new TextEncoder().encode(JSON.stringify(payload));
      let bin = '';
      bytes.forEach((b) => { bin += String.fromCharCode(b); });
      const b64 = btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
      // The fragment is never sent to a server; my control plane reads it.
      window.location.href = '/_seed/#ask=' + b64;
    });

    // screenshot draws the page (without me), cuts out exactly the area my
    // owner selected, and leaves it as a draft; it resolves to the draft's id.
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
      const vw = window.innerWidth, sx = window.scrollX, sy = window.scrollY;
      const root = document.documentElement;
      // Sharp on high-density screens, but never huge.
      const k = Math.min(window.devicePixelRatio || 1, 2, 1600 / r.width, 1600 / r.height);
      const full = await ms.domToCanvas(root, {
        filter: (n) => n !== host,
        width: Math.max(root.scrollWidth, vw),
        height: Math.min(root.scrollHeight, sy + r.top + r.height + 1),
        scale: k,
        backgroundColor: getComputedStyle(document.body).backgroundColor || getComputedStyle(root).backgroundColor || '#fff',
      });
      const out = document.createElement('canvas');
      out.width = Math.max(1, Math.round(r.width * k));
      out.height = Math.max(1, Math.round(r.height * k));
      out.getContext('2d').drawImage(full, (sx + r.left) * k, (sy + r.top) * k, r.width * k, r.height * k, 0, 0, out.width, out.height);
      const blob = await new Promise((resolve) => out.toBlob(resolve, 'image/jpeg', 0.88));
      if (!blob) throw new Error('no image');
      const res = await fetch('/_seed/ask-draft', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'image/jpeg' }, body: blob });
      if (res.status !== 201) throw new Error('not kept');
      const d = await res.json();
      return typeof d.id === 'string' ? d.id : null;
    })());
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('pointerdown', (e) => { if (!ours(e) && isOpen()) closeMenu(); }, true);

    const media = window.matchMedia('print');
    const print = () => { host.style.display = media.matches ? 'none' : ''; };
    media.addEventListener && media.addEventListener('change', print);
    document.body.appendChild(host);
  }).catch(() => {});
})();
