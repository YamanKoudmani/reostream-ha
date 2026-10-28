// Smart rule editor (camera.html "rules"): the rule's line or polygon over
// the live picture, in fractions of the picture ("x,y;x,y" in the hidden
// points input). Drag a point to move it; polygons can gain or lose points
// within the camera's limits.
(function () {
  var NS = 'http://www.w3.org/2000/svg';
  document.querySelectorAll('form.rule').forEach(function (form) {
    var svg = form.querySelector('.rule-svg');
    var line = form.dataset.line === '1';
    var min = +form.dataset.min || (line ? 2 : 4), max = +form.dataset.max || 8;
    var pts = form.points.value.split(';').filter(Boolean).map(function (p) { return p.split(',').map(Number); });
    var dragging = -1;

    function el(name, attrs) {
      var e = document.createElementNS(NS, name);
      for (var k in attrs) e.setAttribute(k, attrs[k]);
      return e;
    }
    function draw() {
      svg.innerHTML = '';
      var d = pts.map(function (p) { return (p[0] * 1000).toFixed(1) + ',' + (p[1] * 1000).toFixed(1); }).join(' ');
      svg.appendChild(el(line ? 'polyline' : 'polygon', { points: d, class: 'rule-shape' }));
      pts.forEach(function (p, i) {
        svg.appendChild(el('circle', { cx: p[0] * 1000, cy: p[1] * 1000, r: 18, class: 'rule-pt', 'data-i': i }));
      });
      form.points.value = pts.map(function (p) { return p[0].toFixed(4) + ',' + p[1].toFixed(4); }).join(';');
      var add = form.querySelector('.rule-addpt'), del = form.querySelector('.rule-delpt');
      if (add) add.disabled = pts.length >= max;
      if (del) del.disabled = pts.length <= min;
    }
    function at(e) {
      var b = svg.getBoundingClientRect();
      return [Math.min(1, Math.max(0, (e.clientX - b.left) / b.width)), Math.min(1, Math.max(0, (e.clientY - b.top) / b.height))];
    }
    svg.addEventListener('pointerdown', function (e) {
      var c = e.target.closest('.rule-pt');
      if (!c) return;
      dragging = +c.getAttribute('data-i');
      svg.setPointerCapture(e.pointerId);
      e.preventDefault();
    });
    svg.addEventListener('pointermove', function (e) {
      if (dragging < 0) return;
      pts[dragging] = at(e);
      draw();
    });
    function end() { dragging = -1; }
    svg.addEventListener('pointerup', end);
    svg.addEventListener('pointercancel', end);
    var add = form.querySelector('.rule-addpt');
    if (add) add.addEventListener('click', function () {
      // A new point halfway along the last edge, back to the first.
      var a = pts[pts.length - 1], b = pts[0];
      pts.push([(a[0] + b[0]) / 2, (a[1] + b[1]) / 2]);
      draw();
    });
    var del = form.querySelector('.rule-delpt');
    if (del) del.addEventListener('click', function () { if (pts.length > min) { pts.pop(); draw(); } });
    draw();
  });
})();
