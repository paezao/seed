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
  document.querySelectorAll('.mock-preview').forEach((el) => play.observe(el));

  // Point and ask, played step by step while it's on screen: hover the
  // seed, the wheel fans out, pick "Change something here", drag a box
  // around the schedule, type, continue.
  const point = document.getElementById('mock-point');
  if (point) {
    const cursor = point.querySelector('.cursor');
    const sel = point.querySelector('.sel');
    const typed = point.querySelector('.typed');
    const text = typed.dataset.text || '';
    const target = point.querySelector('.g-week');
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    const at = (el, fx = 0.5, fy = 0.5) => {
      const r = el.getBoundingClientRect(), R = point.getBoundingClientRect();
      return { x: r.left - R.left + r.width * fx, y: r.top - R.top + r.height * fy };
    };
    const move = (p, ms = 700) => {
      cursor.style.transition = 'transform ' + ms + 'ms cubic-bezier(.45,0,.2,1)';
      cursor.style.transform = 'translate(' + p.x + 'px,' + p.y + 'px)';
      return sleep(ms);
    };
    const click = async () => { cursor.classList.add('press'); await sleep(140); cursor.classList.remove('press'); };
    const state = (...names) => { point.className = 'mock-app gym' + names.map((n) => ' s-' + n).join(''); };
    const reset = () => {
      state();
      typed.textContent = '';
      sel.style.transition = 'none';
      sel.style.width = sel.style.height = '0px';
    };
    if (still) {
      // One still frame of the whole thing.
      const r = at(target, 0, 0), e = at(target, 1, 1);
      Object.assign(sel.style, { left: r.x - 5 + 'px', top: r.y - 5 + 'px', width: e.x - r.x + 10 + 'px', height: e.y - r.y + 10 + 'px' });
      typed.textContent = text;
      state('dim', 'ask');
      cursor.style.display = 'none';
    } else {
      let visible = false, running = false;
      const run = async () => {
        if (running) return;
        running = true;
        while (visible) {
          reset();
          await move({ x: point.clientWidth * 0.45, y: point.clientHeight * 0.7 }, 10);
          await sleep(500);
          const badge = point.querySelector('.seed-badge');
          await move(at(badge, 0.4, 0.6), 900);
          state('hover', 'wheel');
          await sleep(650);
          await move(at(point.querySelector('.w-change em'), 0.5, 0.6), 600);
          state('hover', 'wheel', 'pick');
          await sleep(350);
          await click();
          state('hint', 'pick');
          await sleep(600);
          // Drag a box around the schedule.
          const a = at(target, 0, 0), b = at(target, 1, 1);
          const from = { x: a.x - 6, y: a.y - 6 }, to = { x: b.x + 6, y: b.y + 6 };
          await move(from, 700);
          Object.assign(sel.style, { left: from.x + 'px', top: from.y + 'px', transition: 'none', width: '0px', height: '0px' });
          state('hint', 'dim');
          await sleep(60);
          sel.style.transition = 'width 900ms cubic-bezier(.45,0,.2,1), height 900ms cubic-bezier(.45,0,.2,1)';
          sel.style.width = to.x - from.x + 'px';
          sel.style.height = to.y - from.y + 'px';
          await move(to, 900);
          state('dim', 'ask');
          await sleep(400);
          for (let n = 1; n <= text.length && visible; n++) {
            typed.textContent = text.slice(0, n);
            await sleep(38);
          }
          await sleep(500);
          await move(at(point.querySelector('.ask-go'), 0.5, 0.6), 700);
          state('dim', 'ask', 'go');
          await click();
          await sleep(200);
          state('sent');
          await sleep(2200);
        }
        reset();
        running = false;
      };
      new IntersectionObserver((entries) => {
        visible = entries[0].isIntersecting;
        if (visible) run();
      }, { threshold: 0.35 }).observe(point);
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
