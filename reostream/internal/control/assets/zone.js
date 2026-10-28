// Detection zone painter (camera.html "zone"). The camera divides the
// picture into W x H cells; the mask is W*H characters, row by row from the
// top left, "1" detect and "0" ignore. Drag with the brush to paint cells.
(function () {
  document.querySelectorAll('section.zone').forEach(function (sec) {
    var form = sec.querySelector('form');
    var wrap = sec.querySelector('.zone-wrap');
    var canvas = sec.querySelector('.zone-canvas');
    var save = sec.querySelector('.zone-save');
    var W = +wrap.dataset.w, H = +wrap.dataset.h;
    var mask = form.mask.value.split('');
    var brush = '0', down = false, dirty = false;

    function size() {
      var r = wrap.getBoundingClientRect();
      var dpr = window.devicePixelRatio || 1;
      canvas.width = Math.round(r.width * dpr);
      canvas.height = Math.round(r.height * dpr);
      draw();
    }
    function draw() {
      var ctx = canvas.getContext('2d');
      var cw = canvas.width / W, ch = canvas.height / H;
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      for (var r = 0; r < H; r++) {
        for (var c = 0; c < W; c++) {
          if (mask[r * W + c] === '0') {
            ctx.fillStyle = 'rgba(220, 70, 60, 0.45)';
            ctx.fillRect(c * cw, r * ch, Math.ceil(cw), Math.ceil(ch));
          }
        }
      }
      ctx.strokeStyle = 'rgba(255, 255, 255, 0.08)';
      ctx.lineWidth = 1;
      ctx.beginPath();
      for (var x = 1; x < W; x++) { ctx.moveTo(x * cw, 0); ctx.lineTo(x * cw, canvas.height); }
      for (var y = 1; y < H; y++) { ctx.moveTo(0, y * ch); ctx.lineTo(canvas.width, y * ch); }
      ctx.stroke();
      form.mask.value = mask.join('');
      save.disabled = !dirty;
    }
    function paintAt(e) {
      var r = canvas.getBoundingClientRect();
      var c = Math.floor((e.clientX - r.left) / r.width * W);
      var rr = Math.floor((e.clientY - r.top) / r.height * H);
      if (c < 0 || rr < 0 || c >= W || rr >= H) return;
      // A brush of 3x3 cells: one cell on a 120-wide grid is too fine to aim.
      for (var dy = -1; dy <= 1; dy++) {
        for (var dx = -1; dx <= 1; dx++) {
          var y = rr + dy, x = c + dx;
          if (x >= 0 && y >= 0 && x < W && y < H && mask[y * W + x] !== brush) { mask[y * W + x] = brush; dirty = true; }
        }
      }
      draw();
    }
    canvas.addEventListener('pointerdown', function (e) { down = true; canvas.setPointerCapture(e.pointerId); paintAt(e); e.preventDefault(); });
    canvas.addEventListener('pointermove', function (e) { if (down) paintAt(e); });
    canvas.addEventListener('pointerup', function () { down = false; });
    canvas.addEventListener('pointercancel', function () { down = false; });
    sec.querySelectorAll('input[name="zone-tool"]').forEach(function (i) {
      i.addEventListener('change', function () { if (i.checked) brush = i.value; });
    });
    sec.querySelector('.zone-all').addEventListener('click', function () {
      mask = mask.map(function () { return '1'; }); dirty = true; draw();
    });
    window.addEventListener('resize', size);
    size();
  });
})();
