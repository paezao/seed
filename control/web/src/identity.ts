import { useEffect, useState } from 'react';
import type { Identity } from './api';
import { useLive } from './live';

/** The Seed's current identity, or null until status has loaded. */
export function useIdentity(): Identity | null {
  const { status } = useLive();
  return status?.identity ?? null;
}

/** Parse any CSS color into sRGB 0..255 via the browser. */
function toRgb(color: string): [number, number, number] | null {
  const el = document.createElement('span');
  el.style.color = '';
  el.style.color = color;
  if (!el.style.color) return null;
  el.style.display = 'none';
  document.body.appendChild(el);
  const m = /rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)/.exec(getComputedStyle(el).color);
  el.remove();
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null;
}

function luminance([r, g, b]: [number, number, number]): number {
  const f = (c: number) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

const contrast = (a: number, b: number) => (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);

/** Mix a color toward black (t<0) or white (t>0). */
function shade([r, g, b]: [number, number, number], t: number): [number, number, number] {
  const target = t < 0 ? 0 : 255;
  const k = Math.abs(t);
  return [r + (target - r) * k, g + (target - g) * k, b + (target - b) * k].map(Math.round) as [number, number, number];
}

/** Adjust an accent until it reaches ~4.5:1 contrast against the theme background (also used for link text). */
function fitAccent(rgb: [number, number, number], dark: boolean): [number, number, number] {
  const bg = dark ? luminance([10, 11, 11]) : luminance([250, 250, 249]);
  let c = rgb;
  for (let i = 0; i < 12 && contrast(luminance(c), bg) < 4.5; i++) c = shade(c, dark ? 0.12 : -0.12);
  return c;
}

const css = ([r, g, b]: [number, number, number], a = 1) => (a === 1 ? `rgb(${r}, ${g}, ${b})` : `rgba(${r}, ${g}, ${b}, ${a})`);

function useDarkMode(): boolean {
  const q = '(prefers-color-scheme: dark)';
  const [dark, setDark] = useState(() => window.matchMedia(q).matches);
  useEffect(() => {
    const mq = window.matchMedia(q);
    const on = () => setDark(mq.matches);
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, []);
  return dark;
}

/** Applies identity to the document: title, favicon and accent color. */
export function useApplyIdentity(identity: Identity | null) {
  const dark = useDarkMode();
  const name = identity?.name;
  const logo = identity?.logo_url;
  const accent = identity?.accent;

  useEffect(() => {
    document.title = name ? `${name} · control plane` : 'Control plane';
  }, [name]);

  useEffect(() => {
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!logo) { link?.remove(); return; }
    if (!link) {
      link = document.createElement('link');
      link.rel = 'icon';
      document.head.appendChild(link);
    }
    link.type = 'image/svg+xml';
    link.href = logo;
  }, [logo]);

  useEffect(() => {
    const root = document.documentElement.style;
    const props = ['--accent', '--accent-fg', '--accent-soft', '--accent-line'];
    const rgb = accent ? toRgb(accent) : null;
    if (!rgb) { props.forEach((p) => root.removeProperty(p)); return; }
    const c = fitAccent(rgb, dark);
    const fg = contrast(luminance(c), 1) >= contrast(luminance(c), 0) ? '#ffffff' : '#0a0a0a';
    root.setProperty('--accent', css(c));
    root.setProperty('--accent-fg', fg);
    root.setProperty('--accent-soft', css(c, dark ? 0.1 : 0.08));
    root.setProperty('--accent-line', css(c, dark ? 0.3 : 0.35));
  }, [accent, dark]);
}
