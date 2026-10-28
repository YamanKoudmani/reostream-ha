// Weekly schedule grids (camera.html "schedule"): 7 days x 24 hours, one
// cell per slot, slot = day*24 + hour as the camera stores it. Tap a cell to
// flip it; drag to paint the value the first cell took. Save posts the one
// table the selected tab names.
(function () {
  var DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  document.querySelectorAll('section.sched').forEach(function (sec) {
    var form = sec.querySelector('form');
    var grid = sec.querySelector('.sched-grid');
    var save = sec.querySelector('.sched-save');
    var tabs = Array.prototype.slice.call(sec.querySelectorAll('.sched-tab'));
    var cur = null, table = [], dirty = false, painting = null;

    function cell(i) { return grid.querySelector('[data-slot="' + i + '"]'); }
    function draw() {
      for (var i = 0; i < table.length; i++) {
        var c = cell(i);
        if (c) c.setAttribute('aria-pressed', table[i] === '1' ? 'true' : 'false');
      }
      form.table.value = table.join('');
      save.disabled = !dirty;
    }
    function build(slots) {
      grid.innerHTML = '';
      var days = Math.round(slots / 24) || 7;
      grid.style.gridTemplateColumns = '2.5rem repeat(' + days + ', minmax(0, 1fr))';
      grid.appendChild(document.createElement('span'));
      for (var d = 0; d < days; d++) {
        var h = document.createElement('span');
        h.className = 'sched-day';
        h.textContent = DAYS[d] || ('Day ' + (d + 1));
        grid.appendChild(h);
      }
      for (var hr = 0; hr < 24; hr++) {
        var l = document.createElement('span');
        l.className = 'sched-hour';
        l.textContent = (hr < 10 ? '0' : '') + hr;
        grid.appendChild(l);
        for (var dd = 0; dd < days; dd++) {
          var b = document.createElement('button');
          b.type = 'button';
          b.className = 'sched-cell';
          b.dataset.slot = dd * 24 + hr;
          b.setAttribute('aria-label', (DAYS[dd] || 'Day ' + (dd + 1)) + ' ' + hr + ':00');
          grid.appendChild(b);
        }
      }
    }
    function pick(tab) {
      if (dirty && cur !== tab && !confirm('Discard the unsaved changes to this schedule?')) return;
      tabs.forEach(function (t) { t.setAttribute('aria-selected', t === tab ? 'true' : 'false'); });
      cur = tab;
      table = tab.dataset.table.split('');
      form.type.value = tab.dataset.type;
      form.index.value = tab.dataset.index;
      dirty = false;
      build(table.length);
      draw();
    }
    function set(c, v) {
      var i = +c.dataset.slot;
      if (table[i] !== v) { table[i] = v; dirty = true; draw(); }
    }
    grid.addEventListener('pointerdown', function (e) {
      var c = e.target.closest('.sched-cell');
      if (!c) return;
      painting = table[+c.dataset.slot] === '1' ? '0' : '1';
      set(c, painting);
      e.preventDefault();
    });
    grid.addEventListener('pointerover', function (e) {
      if (painting === null) return;
      var c = e.target.closest('.sched-cell');
      if (c) set(c, painting);
    });
    grid.addEventListener('touchmove', function (e) {
      if (painting === null) return;
      var t = e.touches[0];
      var el = document.elementFromPoint(t.clientX, t.clientY);
      var c = el && el.closest && el.closest('.sched-cell');
      if (c && grid.contains(c)) set(c, painting);
      e.preventDefault();
    }, { passive: false });
    document.addEventListener('pointerup', function () { painting = null; });
    grid.addEventListener('keydown', function (e) {
      var c = e.target.closest('.sched-cell');
      if (c && (e.key === ' ' || e.key === 'Enter')) { set(c, table[+c.dataset.slot] === '1' ? '0' : '1'); e.preventDefault(); }
    });
    sec.querySelector('.sched-all').addEventListener('click', function () { table = table.map(function () { return '1'; }); dirty = true; draw(); });
    sec.querySelector('.sched-none').addEventListener('click', function () { table = table.map(function () { return '0'; }); dirty = true; draw(); });
    tabs.forEach(function (t) { t.addEventListener('click', function () { pick(t); }); });
    var first = tabs.filter(function (t) { return t.getAttribute('aria-selected') === 'true'; })[0] || tabs[0];
    if (first) pick(first);
  });
})();
