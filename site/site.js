// The site's little bits of life. Everything works without it.
(() => {
  const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
  document.documentElement.classList.add('js');

  // Reveal sections as they scroll into view.
  const io = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (e.isIntersecting) {
        e.target.classList.add('in');
        io.unobserve(e.target);
      }
    }
  }, { threshold: 0.15, rootMargin: '0px 0px -40px 0px' });
  document.querySelectorAll('.reveal, .gens').forEach((el) => io.observe(el));

  // The demos play only while visible.
  const play = new IntersectionObserver((entries) => {
    for (const e of entries) e.target.classList.toggle('playing', e.isIntersecting && !still);
  }, { threshold: 0.3 });
  document.querySelectorAll('.mock-app').forEach((el) => play.observe(el));

  // Point and ask: the cursor starts at the seed in the corner, wherever
  // that is at this size.
  const point = document.getElementById('mock-point');
  if (point) {
    const place = () => {
      point.style.setProperty('--bx', (point.clientWidth - 30) + 'px');
      point.style.setProperty('--by', (point.clientHeight - 30) + 'px');
    };
    place();
    new ResizeObserver(place).observe(point);
  }

  // Point and ask: type the request each time the box appears.
  const typed = document.querySelector('#mock-point .typed');
  if (typed) {
    const text = typed.dataset.text || '';
    if (still) {
      typed.textContent = text;
    } else {
      let t0 = performance.now();
      const loop = (now) => {
        const ms = (now - t0) % 9000; // the loop's length (see the CSS)
        const start = 3800, perChar = 45;
        const n = ms < start ? 0 : Math.min(text.length, Math.floor((ms - start) / perChar));
        if (typed.textContent.length !== n) typed.textContent = text.slice(0, n);
        requestAnimationFrame(loop);
      };
      const box = document.getElementById('mock-point');
      new MutationObserver(() => { if (box.classList.contains('playing')) t0 = performance.now(); })
        .observe(box, { attributes: true, attributeFilter: ['class'] });
      requestAnimationFrame(loop);
    }
  }

  // The hero's evolution: its bars, label, cost and badge grow together.
  const phases = ['Planning', 'Evolving', 'Building', 'Testing', 'Launching', 'Checking health', 'Reflecting', 'Applying'];
  const bars = document.querySelectorAll('.mini-evo .phases i');
  const label = document.getElementById('demo-phase');
  const cost = document.getElementById('demo-cost');
  const pill = document.getElementById('demo-pill');
  const done = () => {
    bars.forEach((b) => b.classList.add('on'));
    label.textContent = 'Done';
    cost.textContent = '$0.61';
    pill.textContent = 'complete';
    pill.className = 'pill pill-done';
  };
  if (still) {
    done();
  } else {
    phases.forEach((p, i) => setTimeout(() => {
      bars[i].classList.add('on');
      label.textContent = p + ' · ' + (i + 1) + ' of 8';
      cost.textContent = '$' + (0.076 * (i + 1)).toFixed(2);
    }, 900 + i * 380));
    setTimeout(done, 900 + phases.length * 380 + 200);
  }

  // The floating cards drift a little with the pointer.
  const stage = document.getElementById('stage');
  if (stage && !still && matchMedia('(pointer: fine)').matches) {
    const floats = stage.querySelectorAll('.float');
    window.addEventListener('pointermove', (e) => {
      const x = e.clientX / window.innerWidth - 0.5, y = e.clientY / window.innerHeight - 0.5;
      floats.forEach((f) => {
        const d = Number(f.dataset.depth || 10);
        f.style.transform = 'translate(' + (-x * d) + 'px,' + (-y * d) + 'px)';
      });
    }, { passive: true });
  }

  // Copy buttons.
  for (const b of document.querySelectorAll('[data-copy]')) {
    b.addEventListener('click', async () => {
      const text = document.getElementById(b.dataset.copy).textContent;
      try {
        await navigator.clipboard.writeText(text);
        b.textContent = 'Copied';
      } catch {
        b.textContent = 'Select it';
      }
      setTimeout(() => { b.textContent = 'Copy'; }, 1600);
    });
  }
})();
