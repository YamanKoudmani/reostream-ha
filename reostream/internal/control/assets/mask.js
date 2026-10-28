// Privacy mask boxes (camera.html "mask"). Boxes are fractions of the
// picture; "x,y,w,h;..." in the hidden input is what Save posts.
(function () {
  document.querySelectorAll('section.mask').forEach(function (sec) {
    var form = sec.querySelector('form');
    var wrap = sec.querySelector('.mask-wrap');
    var layer = sec.querySelector('.mask-layer');
    var save = sec.querySelector('.mask-save');
    var remove = sec.querySelector('.mask-remove');
    var max = +wrap.dataset.max || 4;
    var rects = form.rects.value ? form.rects.value.split(';').map(function (p) { return p.split(',').map(Number); }) : [];
    var sel = -1, drag = null, dirty = false;

    function draw() {
      layer.innerHTML = '';
      rects.forEach(function (r, i) {
        var d = document.createElement('div');
        d.className = 'mask-box' + (i === sel ? ' selected' : '');
        d.style.left = r[0] * 100 + '%'; d.style.top = r[1] * 100 + '%';
        d.style.width = r[2] * 100 + '%'; d.style.height = r[3] * 100 + '%';
        d.dataset.i = i;
        layer.appendChild(d);
      });
      form.rects.value = rects.map(function (r) { return r.map(function (v) { return v.toFixed(4); }).join(','); }).join(';');
      save.disabled = !dirty;
      remove.disabled = sel < 0;
    }
    function at(e) {
      var b = layer.getBoundingClientRect();
      return [Math.min(1, Math.max(0, (e.clientX - b.left) / b.width)), Math.min(1, Math.max(0, (e.clientY - b.top) / b.height))];
    }
    layer.addEventListener('pointerdown', function (e) {
      var box = e.target.closest('.mask-box');
      if (box) { sel = +box.dataset.i; draw(); return; }
      if (rects.length >= max) { sel = -1; draw(); return; }
      var p = at(e);
      drag = { x: p[0], y: p[1] };
      rects.push([p[0], p[1], 0, 0]);
      sel = rects.length - 1;
      layer.setPointerCapture(e.pointerId);
      e.preventDefault();
    });
    layer.addEventListener('pointermove', function (e) {
      if (!drag) return;
      var p = at(e), r = rects[sel];
      r[0] = Math.min(drag.x, p[0]); r[1] = Math.min(drag.y, p[1]);
      r[2] = Math.abs(p[0] - drag.x); r[3] = Math.abs(p[1] - drag.y);
      draw();
    });
    function end() {
      if (!drag) return;
      drag = null;
      var r = rects[sel];
      if (r[2] < 0.02 || r[3] < 0.02) { rects.splice(sel, 1); sel = -1; } else { dirty = true; }
      draw();
    }
    layer.addEventListener('pointerup', end);
    layer.addEventListener('pointercancel', end);
    remove.addEventListener('click', function () { if (sel >= 0) { rects.splice(sel, 1); sel = -1; dirty = true; draw(); } });
    sec.querySelector('.mask-clear').addEventListener('click', function () { rects = []; sel = -1; dirty = true; draw(); });
    draw();
  });
})();
