import { useMemo } from 'react';
import { Marked, type Tokens } from 'marked';

const escapeHtml = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');

const SAFE_URL = /^(https?:|mailto:|\/|#|\.{0,2}\/|[^:]*$)/i;
const safeUrl = (href: string) => (SAFE_URL.test(href.trim()) ? href : '#');

// Raw HTML in markdown is rendered as text, and links/images are restricted to
// safe protocols. Content comes from an LLM-driven agent, so treat it as untrusted.
const md = new Marked({ gfm: true, breaks: false });
md.use({
  renderer: {
    html({ text }: Tokens.HTML | Tokens.Tag) {
      return escapeHtml(text);
    },
    link(this: { parser: { parseInline: (t: Tokens.Link['tokens']) => string } }, { href, title, tokens }: Tokens.Link) {
      const inner = this.parser.parseInline(tokens);
      const t = title ? ` title="${escapeHtml(title)}"` : '';
      const external = /^https?:/i.test(href) ? ' target="_blank" rel="noopener noreferrer"' : '';
      return `<a href="${escapeHtml(safeUrl(href))}"${t}${external}>${inner}</a>`;
    },
    image({ href, title, text }: Tokens.Image) {
      const t = title ? ` title="${escapeHtml(title)}"` : '';
      return `<img src="${escapeHtml(safeUrl(href))}" alt="${escapeHtml(text)}"${t} loading="lazy">`;
    },
  },
});

export function Markdown({ source, className }: { source: string; className?: string }) {
  const html = useMemo(() => md.parse(source || '', { async: false }) as string, [source]);
  return <div className={`md ${className ?? ''}`} dangerouslySetInnerHTML={{ __html: html }} />;
}
