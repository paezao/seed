// Command docsgen renders docs/*.md into the website (site/docs/*.html):
// the site's look, a sidebar of every doc, a table of contents, links
// between docs kept working and links into the code sent to GitHub.
//
//	go run . -docs ../../../docs -out ../../docs
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

const repo = "https://github.com/paezao/seed"

type doc struct{ file, title, about string }

// The docs, grouped as the sidebar shows them.
var groups = []struct {
	name string
	docs []doc
}{
	{"Start here", []doc{
		{"philosophy", "Philosophy", "Why Seed exists"},
		{"architecture", "Architecture", "Kernel, control plane, organism"},
		{"development", "Development", "Running, testing, contributing"},
	}},
	{"How it grows", []doc{
		{"evolution", "Evolution lifecycle", "How software modifies itself safely"},
		{"skills", "Skills", "Reusable capabilities a Seed acquires"},
		{"knowledge", "Knowledge", "Continuity of identity"},
	}},
	{"Living with a Seed", []doc{
		{"health", "Health", "Noticing, diagnosing and fixing problems"},
		{"routines", "Routines", "What it does on a schedule"},
		{"spending", "Spending", "What its model calls cost, and a budget"},
		{"backups", "Backups", "Copies of the data, restoring, rolling back"},
		{"notifications", "Notifications", "Web Push when it needs you"},
	}},
	{"Running one", []doc{
		{"hosting", "Hosting", "A volume and an external PostgreSQL"},
		{"updates", "Kernel updates", "Signed releases and automatic rollback"},
		{"releasing", "Releasing", "How a version reaches people"},
		{"security", "Security", "Sandboxing, permissions, the kernel boundary"},
		{"api", "Control plane API", "Every endpoint"},
	}},
}

func main() {
	docsDir := flag.String("docs", "docs", "the markdown docs")
	out := flag.String("out", "site/docs", "where the HTML goes")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
	)
	var all []doc
	for _, g := range groups {
		all = append(all, g.docs...)
	}
	for _, d := range all {
		src, err := os.ReadFile(filepath.Join(*docsDir, d.file+".md"))
		if err != nil {
			log.Fatalf("%s: %v", d.file, err)
		}
		page, err := render(md, d, all, src)
		if err != nil {
			log.Fatalf("%s: %v", d.file, err)
		}
		if err := os.WriteFile(filepath.Join(*out, d.file+".html"), page, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "index.html"), index(all), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rendered %d docs into %s\n", len(all), *out)
}

type heading struct {
	level    int
	id, text string
}

func render(md goldmark.Markdown, d doc, all []doc, src []byte) ([]byte, error) {
	root := md.Parser().Parse(text.NewReader(src))
	var toc []heading
	title := d.title
	err := ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			id, _ := n.AttributeString("id")
			t := plain(n, src)
			if n.Level == 1 {
				title = t
			} else if n.Level <= 3 {
				toc = append(toc, heading{n.Level, fmt.Sprint(string(id.([]byte))), t})
			}
		case *ast.Link:
			n.Destination = []byte(rewrite(string(n.Destination)))
		case *ast.Image:
			n.Destination = []byte(rewrite(string(n.Destination)))
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := md.Renderer().Render(&body, src, root); err != nil {
		return nil, err
	}
	content := mermaidRe.ReplaceAllStringFunc(body.String(), func(m string) string {
		inner := mermaidRe.FindStringSubmatch(m)[1]
		return `<pre class="mermaid">` + inner + `</pre>`
	})
	hasMermaid := strings.Contains(content, `class="mermaid"`)
	return page(title, d, all, toc, content, hasMermaid), nil
}

// mermaidScript draws the diagrams in the site's colours (light or dark), at
// a readable size (wide ones scroll), each with a full-screen view.
const mermaidScript = `  <dialog class="diagram-dialog" id="diagram-dialog"><button class="diagram-close" aria-label="Close">×</button><div class="diagram-big"></div></dialog>
  <script type="module">
    import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';
    const css = getComputedStyle(document.documentElement);
    const v = (n) => css.getPropertyValue(n).trim();
    const dark = matchMedia('(prefers-color-scheme: dark)').matches;
    mermaid.initialize({
      startOnLoad: false,
      theme: 'base',
      fontFamily: 'Inter, ui-sans-serif, system-ui, sans-serif',
      flowchart: { useMaxWidth: true, htmlLabels: true, curve: 'basis', padding: 12, nodeSpacing: 28, rankSpacing: 46 },
      state: { useMaxWidth: true },
      themeVariables: {
        fontSize: '15px',
        background: v('--surface'),
        primaryColor: v('--surface-2'),
        primaryTextColor: v('--fg'),
        primaryBorderColor: v('--accent'),
        secondaryColor: v('--surface'),
        tertiaryColor: v('--bg-sub'),
        lineColor: v('--muted'),
        textColor: v('--fg'),
        clusterBkg: dark ? 'rgba(74,222,128,0.05)' : 'rgba(21,128,61,0.04)',
        clusterBorder: v('--border-strong'),
        edgeLabelBackground: v('--surface'),
        nodeTextColor: v('--fg'),
        labelBackgroundColor: v('--surface'),
        stateLabelColor: v('--fg'),
        transitionColor: v('--muted'),
        transitionLabelColor: v('--fg-2'),
        noteBkgColor: v('--surface-2'),
        noteTextColor: v('--fg'),
        noteBorderColor: v('--border-strong'),
      },
    });
    const blocks = [...document.querySelectorAll('pre.mermaid')];
    for (const pre of blocks) {
      const fig = document.createElement('figure');
      fig.className = 'diagram';
      pre.replaceWith(fig);
      const scroll = document.createElement('div');
      scroll.className = 'diagram-scroll';
      scroll.appendChild(pre);
      const expand = document.createElement('button');
      expand.className = 'diagram-expand';
      expand.type = 'button';
      expand.textContent = 'Expand';
      fig.append(scroll, expand);
    }
    await mermaid.run({ nodes: blocks });
    const dialog = document.getElementById('diagram-dialog');
    const big = dialog.querySelector('.diagram-big');
    for (const fig of document.querySelectorAll('figure.diagram')) {
      fig.querySelector('.diagram-expand').addEventListener('click', () => {
        // Fit the whole diagram on the screen, centred (at most twice its size).
        const svg = fig.querySelector('svg').cloneNode(true);
        const vb = svg.viewBox.baseVal;
        svg.removeAttribute('width');
        svg.removeAttribute('height');
        svg.removeAttribute('style');
        big.replaceChildren(svg);
        dialog.showModal();
        const room = big.getBoundingClientRect();
        const k = Math.min((room.width - 56) / vb.width, (window.innerHeight * 0.92 - 90) / vb.height, 2);
        svg.style.width = Math.round(vb.width * k) + 'px';
        svg.style.height = Math.round(vb.height * k) + 'px';
      });
    }
    dialog.querySelector('.diagram-close').addEventListener('click', () => dialog.close());
    dialog.addEventListener('click', (e) => { if (e.target === dialog) dialog.close(); });
  </script>
`

var mermaidRe = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(src))
			}
			if t, ok := c.(*ast.String); ok {
				b.Write(t.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

var docLink = regexp.MustCompile(`^([a-z0-9-]+)\.md(#.*)?$`)

// rewrite keeps links between docs on the site and sends the rest of the
// repository to GitHub.
func rewrite(dest string) string {
	switch {
	case dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:"):
		return dest
	case docLink.MatchString(dest):
		m := docLink.FindStringSubmatch(dest)
		return m[1] + ".html" + m[2]
	}
	p := filepath.ToSlash(filepath.Clean(filepath.Join("docs", dest)))
	if strings.HasPrefix(p, "../") {
		return dest
	}
	frag := ""
	if i := strings.Index(p, "#"); i >= 0 {
		p, frag = p[:i], p[i:]
	}
	kind := "blob"
	if !strings.Contains(filepath.Base(p), ".") {
		kind = "tree"
	}
	return repo + "/" + kind + "/main/" + p + frag
}

const seedSVG = `<svg class="mark" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 21v-9" class="stem"/><path d="M12 13c0-4.4 2.9-7.3 7.8-7.3 0 4.4-2.9 7.3-7.8 7.3z" class="leaf"/><path d="M12 15.5c0-3.3-2.2-5.6-5.8-5.6 0 3.3 2.2 5.6 5.8 5.6z" class="leaf leaf-2"/></svg>`

func head(title, desc string) string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>` + html.EscapeString(title) + ` · Seed docs</title>
  <meta name="description" content="` + html.EscapeString(desc) + `">
  <meta property="og:image" content="https://paezao.github.io/seed/assets/og.png">
  <meta name="twitter:card" content="summary_large_image">
  <link rel="icon" href="../assets/seed.svg" type="image/svg+xml">
  <link rel="stylesheet" href="../style.css">
</head>
<body class="docs-body">
  <div class="aurora aurora-quiet" aria-hidden="true"><i class="a1"></i><i class="a2"></i></div>
  <header class="nav">
    <div class="wrap wrap-wide nav-inner">
      <a class="brand" href="../" aria-label="Seed home">` + seedSVG + `<span>Seed</span><span class="brand-sub">docs</span></a>
      <nav class="nav-links">
        <a href="../#how">How it grows</a>
        <a href="../#features">Features</a>
        <a href="./">Docs</a>
        <a class="btn btn-ghost btn-sm" href="` + repo + `">GitHub</a>
      </nav>
    </div>
  </header>
`
}

func sidebar(current string) string {
	var b strings.Builder
	b.WriteString(`<nav class="docs-nav" aria-label="Documentation">`)
	for _, g := range groups {
		b.WriteString(`<p class="docs-group">` + html.EscapeString(g.name) + `</p><ul>`)
		for _, d := range g.docs {
			cls := ""
			if d.file == current {
				cls = ` class="current" aria-current="page"`
			}
			fmt.Fprintf(&b, `<li><a href="%s.html"%s>%s</a></li>`, d.file, cls, html.EscapeString(d.title))
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</nav>`)
	return b.String()
}

func page(title string, d doc, all []doc, toc []heading, content string, mermaid bool) []byte {
	var b strings.Builder
	b.WriteString(head(title, d.about))
	b.WriteString(`<main class="wrap wrap-wide docs-layout">` + sidebar(d.file) + `<article class="prose">`)
	b.WriteString(content)
	// Previous and next, in sidebar order.
	for i, x := range all {
		if x.file != d.file {
			continue
		}
		b.WriteString(`<nav class="docs-pager">`)
		if i > 0 {
			fmt.Fprintf(&b, `<a class="prev" href="%s.html"><span>Previous</span>%s</a>`, all[i-1].file, html.EscapeString(all[i-1].title))
		} else {
			b.WriteString(`<span></span>`)
		}
		if i+1 < len(all) {
			fmt.Fprintf(&b, `<a class="next" href="%s.html"><span>Next</span>%s</a>`, all[i+1].file, html.EscapeString(all[i+1].title))
		}
		b.WriteString(`</nav>`)
	}
	fmt.Fprintf(&b, `<p class="docs-edit"><a href="%s/edit/main/docs/%s.md">Edit this page on GitHub</a></p>`, repo, d.file)
	b.WriteString(`</article>`)
	if len(toc) > 1 {
		b.WriteString(`<aside class="docs-toc"><p>On this page</p><ul>`)
		for _, h := range toc {
			fmt.Fprintf(&b, `<li class="toc-%d"><a href="#%s">%s</a></li>`, h.level, html.EscapeString(h.id), html.EscapeString(h.text))
		}
		b.WriteString(`</ul></aside>`)
	}
	b.WriteString(`</main>`)
	b.WriteString(foot(mermaid))
	return []byte(b.String())
}

func index(all []doc) []byte {
	var b strings.Builder
	b.WriteString(head("Documentation", "How Seed works, and how to live with one."))
	b.WriteString(`<main class="wrap wrap-wide docs-layout docs-home">` + sidebar("") + `<article class="prose">`)
	b.WriteString(`<p class="eyebrow">Documentation</p><h1>How Seed works</h1><p class="lead-sm">A Seed is a minimal, self-modifying system: a kernel, a control plane and an empty body that grows into your app through conversation. Start with the philosophy and architecture, or jump to what you need.</p>`)
	for _, g := range groups {
		b.WriteString(`<h2>` + html.EscapeString(g.name) + `</h2><div class="docs-cards">`)
		for _, d := range g.docs {
			fmt.Fprintf(&b, `<a class="docs-card" href="%s.html"><b>%s</b><span>%s</span></a>`, d.file, html.EscapeString(d.title), html.EscapeString(d.about))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</article></main>`)
	b.WriteString(foot(false))
	return []byte(b.String())
}

func foot(mermaid bool) string {
	s := `
  <footer class="footer">
    <div class="wrap wrap-wide footer-inner">
      <span class="brand brand-sm">` + seedSVG + ` Seed</span>
      <span class="muted small">MPL-2.0 · <a href="` + repo + `">GitHub</a> · <a href="./">Docs</a></span>
    </div>
  </footer>
`
	if mermaid {
		s += mermaidScript
	}
	return s + "</body>\n</html>\n"
}
