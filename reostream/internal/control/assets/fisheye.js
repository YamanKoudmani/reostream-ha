// fisheye.js: dewarps a fisheye camera's circular picture in the browser.
//
// The camera never dewarps anything for us. Measured against a live 360
// camera: while the phone app was drawing a wide defished panorama, the
// camera was sending imageType 0, a raw 2560x2560 circle, and changing the
// app's view mode wrote nothing back to the camera -- a diff of all 44 of
// its config blocks across a mode change moved only the clock and the CPU
// gauge. So the view is ours to draw, and this file draws it.
//
// The vendor's own client does this with a textured 3D mesh per view mode
// and a pixel shader that only converts YUV to RGB. We do the inverse and
// keep the geometry in the fragment shader: one screen-filling triangle
// pair, and for every output pixel we work out which direction it looks
// and sample the circle there. That is fewer moving parts, it costs one
// draw call per pane, and it does not quantize the picture onto a mesh.

(function () {
  'use strict';

  // The lens maps angle-from-axis linearly onto radius -- an equidistant
  // fisheye, which is what these sensors are. u_halfFov is half the lens's
  // total field of view: a 180 degree lens fills the circle at pi/2.
  //
  // That is the lens, and it is fixed. The PROJECTION is the other end of
  // the same ray and is ours to choose: it decides which direction each
  // output pixel looks. Changing it is a different ray function and nothing
  // else -- no new mesh, no new draw call, no second shader.
  var VERT = [
    'attribute vec2 a_pos;',
    'varying vec2 v_ndc;',
    'void main() {',
    '  v_ndc = a_pos;',
    '  gl_Position = vec4(a_pos, 0.0, 1.0);',
    '}'
  ].join('\n');

  var FRAG = [
    'precision highp float;',
    'varying vec2 v_ndc;',
    'uniform sampler2D u_tex;',
    'uniform vec2  u_center;',   // circle center, texture units
    'uniform float u_radius;',   // circle radius, texture units
    'uniform float u_halfFov;',  // half the lens field of view, radians
    'uniform float u_yaw;',
    'uniform float u_pitch;',
    'uniform float u_zoom;',     // half the virtual camera's fov, radians
    'uniform float u_aspect;',
    'uniform float u_flip;',      // -1 mirrors the picture; see setMount
    'uniform float u_ceiling;',   // 1 mounted on the ceiling, 0 on a wall
    'uniform float u_proj;',     // 0 rectilinear, 1 stereographic, 2 band
    '',
    'void main() {',
    // The ray this output pixel looks along, in the virtual camera's own
    // frame, before it is aimed anywhere.
    //
    // Only the angle out from the pane's center depends on the projection;
    // the bearing around that center never does, which is why both branches
    // share p/q below and differ in one line. Both are written so that the
    // pane's vertical edge sits at exactly u_zoom from the center whichever
    // is chosen, so switching projection does not also change the zoom.
    //
    //   rectilinear    plane radius = tan(theta)      inverse: atan(q*tan(z))
    //   stereographic  plane radius = 2*tan(theta/2)  inverse: 2*atan(q*tan(z/2))
    //
    // Every projection ends in the same two numbers: how far from the lens
    // axis this pixel looks, and which way round. The circle is sampled
    // from those once, at the bottom, so a new projection is a new way of
    // producing them and touches nothing else.
    '  float theta; vec2 dir;',
    '',
    '  if (u_proj > 1.5) {',
    // The band. Not a pinhole camera at all: the two screen axes ARE the
    // two angles. Across is the way round the lens axis, up and down is the
    // angle out from it, so the whole ring the lens saw unrolls into a
    // strip -- which is what the vendor calls dual panoramic when it cuts
    // the ring in two and stacks the halves. Here that is the ordinary
    // two-pane layout, because each pane already carries its own yaw.
    //
    // u_zoom is half the azimuth the pane spans here, not half a field of
    // view, and the vertical span is derived from it rather than given: at
    // u_zoom/u_aspect the degrees per pixel come out the same both ways, so
    // the strip is drawn in proportion and letterboxes in a pane too tall
    // for it instead of stretching to fill.
    //
    // Which axis it unrolls AROUND is the mount, and this is the one place
    // in the file where that is not cosmetic. A ceiling camera's lens axis
    // points down the middle of the room, so the ring around it is the
    // horizon and unrolling it gives the panorama. A camera on a wall looks
    // along the room instead: the ring around ITS lens axis is a vertical
    // hoop, and unrolling that stands the room on its side. The band a wall
    // camera wants runs around the room's upright, which is the picture's
    // own up -- the lens frame's y, not its z.
    //
    // The two differ in where phi starts as well as what it turns about. On
    // the ring, phi 0 is an arbitrary spoke and the band wraps the whole way
    // round. On the wall band, phi 0 has to be the way the camera is LOOKING,
    // because the hemisphere it saw is the half in front of it: start phi on
    // the lens axis and the strip is the picture, start it a quarter turn off
    // and half the strip is black.
    '    float phi = u_yaw + v_ndc.x * u_zoom;',
    // Up the screen is away from whatever the strip is unrolled around, and
    // that axis points opposite ways in the two mounts: a ceiling lens looks
    // DOWN, so moving up the strip means moving out toward the horizon,
    // while a wall band is measured from the room's upright, where moving up
    // means moving toward it. Get this backwards and the panorama is
    // upside down while every other number in it still checks out.
    '    float vsign = u_ceiling > 0.5 ? 1.0 : -1.0;',
    '    float tb  = u_pitch + vsign * v_ndc.y * (u_zoom / u_aspect);',
    '    if (tb < 0.0 || tb > 3.14159265) { gl_FragColor = vec4(0.0, 0.0, 0.0, 1.0); return; }',
    '    vec3 r = u_ceiling > 0.5',
    '      ? vec3(sin(tb) * cos(phi), sin(tb) * sin(phi), cos(tb))',
    '      : vec3(sin(tb) * sin(phi), cos(tb), sin(tb) * cos(phi));',
    '    theta = acos(clamp(r.z, -1.0, 1.0));',
    '    if (theta > u_halfFov) { gl_FragColor = vec4(0.0, 0.0, 0.0, 1.0); return; }',
    '    float sb = length(r.xy);',
    '    dir = sb > 0.0001 ? vec2(r.x, -r.y) / sb : vec2(0.0, 0.0);',
    '  } else {',
    // The ray this output pixel looks along, in the virtual camera's own
    // frame, before it is aimed anywhere.
    //
    // Only the angle out from the pane's center depends on which of these
    // two it is; the bearing around that center does not, which is why the
    // branch is one line. Both are scaled so the pane's vertical edge sits
    // at exactly u_zoom from the center, so switching between them does not
    // also change the zoom.
    //
    //   rectilinear    plane radius = tan(theta)      inverse: atan(q*tan(z))
    //   stereographic  plane radius = 2*tan(theta/2)  inverse: 2*atan(q*tan(z/2))
    '    vec2 p = vec2(v_ndc.x * u_aspect, v_ndc.y);',
    '    float q = length(p);',
    '    float theta0 = u_proj < 0.5',
    '      ? atan(q * tan(u_zoom))',
    '      : 2.0 * atan(q * tan(u_zoom * 0.5));',
    // At dead center the bearing is undefined and q cancels nothing, so the
    // ray is the axis itself rather than a division by zero.
    '    vec3 d = q > 0.000001',
    '      ? vec3(p / q * sin(theta0), cos(theta0))',
    '      : vec3(0.0, 0.0, 1.0);',
    // Aim it: pitch tilts away from the lens axis, yaw spins around it.
    '    float cp = cos(u_pitch), sp = sin(u_pitch);',
    '    vec3 r = vec3(d.x, d.y * cp - d.z * sp, d.y * sp + d.z * cp);',
    '    float cy = cos(u_yaw), sy = sin(u_yaw);',
    '    r = vec3(r.x * cy - r.y * sy, r.x * sy + r.y * cy, r.z);',
    '    r.y *= u_flip;',
    '    theta = acos(clamp(r.z, -1.0, 1.0));',
    '    if (theta > u_halfFov) { gl_FragColor = vec4(0.0, 0.0, 0.0, 1.0); return; }',
    '    float s = length(r.xy);',
    // Texture space runs DOWN: v = 0 is the first row uploaded, which is the
    // top of the frame, while the ray's y runs up. Negating y here is the
    // one place those two conventions are reconciled -- doing it by flipping
    // the upload instead would silently invert the ceiling/wall mount flip
    // as well, since that works on r.y too. The band's dir negates its own
    // sine for the same reason.
    '    dir = s > 0.0001 ? vec2(r.x, -r.y) / s : vec2(0.0, 0.0);',
    '  }',
    '',
    '  vec2 uv = u_center + dir * (theta / u_halfFov) * u_radius;',
    '  if (uv.x < 0.0 || uv.x > 1.0 || uv.y < 0.0 || uv.y > 1.0) {',
    '    gl_FragColor = vec4(0.0, 0.0, 0.0, 1.0); return;',
    '  }',
    '  gl_FragColor = texture2D(u_tex, uv);',
    '}'
  ].join('\n');

  function compile(gl, type, src) {
    var sh = gl.createShader(type);
    gl.shaderSource(sh, src);
    gl.compileShader(sh);
    if (!gl.getShaderParameter(sh, gl.COMPILE_STATUS)) {
      throw new Error(gl.getShaderInfoLog(sh) || 'shader failed to compile');
    }
    return sh;
  }

  // A View is one pane: its own aim, sharing one texture with every other
  // pane. Quad view is four of these, which is why aim is per-view and the
  // frame is not.
  function View() {
    this.yaw = 0;
    this.pitch = 0;
    this.zoom = 0.6;      // radians, half-angle; about 69 degrees across
  }

  // What a pane opens to when it is reset, per projection. The band's entry
  // is for a single pane and is divided by the pane count, because its zoom
  // is an azimuth span: two panes each carry half the ring, four a quarter,
  // and together they are always the whole of it. Rectilinear is
  // held narrow because it has to be; stereographic is opened wide because
  // being able to is the entire reason it exists.
  //
  // 0.9 is not a taste: it is where a widescreen pane's CORNERS land exactly
  // on the rim of a 180 degree circle. A 16:9 pane's corner is 2.04 times its
  // half height out, and stereographic puts that at 2*atan(2.04*tan(z/2)),
  // which reaches 90 degrees from the axis at z = 0.911. Opening wider is
  // allowed and sometimes wanted -- it is the whole hemisphere, vignetted
  // into a circle -- but it should not be what a reset hands you.
  View.defaultZoom = [0.6, 0.9, Math.PI];

  View.prototype.aim = function (yaw, pitch) {
    this.yaw = yaw;
    this.pitch = Math.max(0, Math.min(Math.PI / 2, pitch));
  };

  // Renderer owns the GL context, the one texture the video is uploaded
  // into, and the panes drawn from it.
  function Renderer(canvas, video) {
    var gl = canvas.getContext('webgl', { alpha: false, antialias: true });
    if (!gl) throw new Error('this browser has no WebGL');
    this.gl = gl;
    this.canvas = canvas;
    this.video = video;

    var prog = gl.createProgram();
    gl.attachShader(prog, compile(gl, gl.VERTEX_SHADER, VERT));
    gl.attachShader(prog, compile(gl, gl.FRAGMENT_SHADER, FRAG));
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) {
      throw new Error(gl.getProgramInfoLog(prog) || 'program failed to link');
    }
    gl.useProgram(prog);
    this.prog = prog;

    var buf = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    gl.bufferData(gl.ARRAY_BUFFER,
      new Float32Array([-1, -1, 1, -1, -1, 1, -1, 1, 1, -1, 1, 1]),
      gl.STATIC_DRAW);
    var loc = gl.getAttribLocation(prog, 'a_pos');
    gl.enableVertexAttribArray(loc);
    gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);

    this.u = {};
    var names = ['u_tex', 'u_center', 'u_radius', 'u_halfFov', 'u_yaw',
                 'u_pitch', 'u_zoom', 'u_aspect', 'u_flip', 'u_proj',
                 'u_ceiling'];
    for (var i = 0; i < names.length; i++) {
      this.u[names[i]] = gl.getUniformLocation(prog, names[i]);
    }

    this.tex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, this.tex);
    // The video is not a power of two, so mips and repeat are both out.
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);

    // Sensible defaults for a circle inscribed in a square frame. The page
    // exposes these because a lens is not always centered and not always
    // exactly 180 degrees, and a few pixels out is visible at the rim.
    this.center = [0.5, 0.5];
    this.radius = 0.5;
    this.halfFov = Math.PI / 2;
    this.ceiling = true;
    this.flip = -1;
    this.projection = 0;

    this.views = [new View()];
  }

  // How wide a pane may be opened, which is not a matter of taste: past
  // these the picture stops being worth drawing. Rectilinear's corners run
  // away, and no projection can show more than the lens saw, so a
  // stereographic pane stops where the circle does.
  Renderer.prototype.maxZoom = function () {
    if (this.projection === 2) return Math.PI;   // the whole ring, once
    return this.projection === 0 ? 1.35 : this.halfFov;
  };

  // paneZoom is what one pane of n opens to. Only the band divides: its
  // zoom is a share of the ring rather than a field of view, so the panes
  // of a layout tile the circle instead of repeating it.
  Renderer.prototype.paneZoom = function (n) {
    var z = View.defaultZoom[this.projection];
    if (this.projection !== 2) return z;
    // Half a ring for a wall mount, because the half behind the camera is
    // not in the picture: a full sweep there buys a pane of black.
    return (this.ceiling ? z : z / 2) / n;
  };

  // bandCenter is where the middle of a strip sits, measured from whichever
  // axis the band is unrolled around. A ceiling band is centered halfway out
  // to the rim; a wall band is centered on the horizon, which is a quarter
  // turn from the upright it runs around.
  Renderer.prototype.bandCenter = function () {
    return this.ceiling ? this.halfFov / 2 : Math.PI / 2;
  };

  // setMount says where the camera is, and that decides two separate
  // things, which is why it is one call and not a flag anyone sets by hand.
  //
  // A band unrolls around the lens axis from a ceiling and around the
  // room's upright from a wall -- see the shader. And a lens pointing DOWN
  // hands back a picture of the opposite handedness to one pointing along
  // the room, so the ceiling case is mirrored and the wall case is not.
  //
  // That is the opposite of what this page did until it was looked at. The
  // one fisheye here is on a wall, and its picture only came out upright on
  // the setting labeled Ceiling -- which is the same thing as saying the
  // two were the wrong way round. The wall half of this is measured; the
  // ceiling half is the mirror of it, and wants a real ceiling camera in
  // front of an operator before anyone calls it confirmed.
  Renderer.prototype.setMount = function (where) {
    // Both spellings are named, and an unknown one falls back to the
    // default rather than silently meaning "wall". The page's select is the
    // only caller, so these two strings and its two option values are one
    // thing in two files; a test holds them together.
    if (where !== 'ceiling' && where !== 'wall') where = 'ceiling';
    this.ceiling = where === 'ceiling';
    this.flip = this.ceiling ? -1 : 1;
    // A band's width and center are both mount-dependent, so the panes are
    // laid out again rather than left pointing at where the other mount's
    // strip used to be.
    if (this.projection === 2) this.setLayout(this.views.length);
  };

  // Switching projection keeps where each pane is pointing and how wide it
  // is open, so the operator sees the same scene drawn differently rather
  // than a new view. Only a zoom the new projection cannot hold is moved.
  Renderer.prototype.setProjection = function (p) {
    var was = this.projection;
    this.projection = p === 1 ? 1 : (p === 2 ? 2 : 0);
    // Crossing into or out of the band is the one switch that cannot keep
    // the panes as they are: there a zoom is an azimuth span and a pitch is
    // the middle of a strip, and the same numbers mean something else on
    // either side. Between the two pinhole projections they mean the same
    // thing and are kept.
    if (was === 2 || this.projection === 2) {
      this.setLayout(this.views.length);
      return;
    }
    var max = this.maxZoom();
    for (var i = 0; i < this.views.length; i++) {
      this.views[i].zoom = Math.min(this.views[i].zoom, max);
    }
  };

  Renderer.prototype.setLayout = function (n) {
    while (this.views.length < n) this.views.push(new View());
    this.views.length = n;
    for (var j = 0; j < n; j++) {
      this.views[j].zoom = this.paneZoom(n);
    }
    // Fan the panes out so a fresh quad view looks at four different
    // things rather than four copies of the same wall.
    //
    // How far out they are tilted is not a constant, it is whatever leaves
    // the top of a pane on the rim of the circle: tilt further and a fresh
    // quad view opens with a black band across the top of every pane. The
    // old constant 1.0 happened to be that answer for a 0.6 pane and stops
    // being it the moment a wider projection is chosen.
    //
    // The band aims differently in both axes: its panes are slices of the
    // ring rather than directions tilted out of it, so every pane sits at
    // the middle of the strip and only the yaw differs. A single band pane
    // is not a special case either -- one pane is the whole ring.
    if (this.projection === 2) {
      var sweep = (this.ceiling ? 2 * Math.PI : Math.PI) / n;
      for (var k = 0; k < n; k++) {
        this.views[k].aim((k - (n - 1) / 2) * sweep, this.bandCenter());
      }
    } else if (n > 1) {
      var tilt = Math.max(0, this.halfFov - View.defaultZoom[this.projection]);
      for (var i = 0; i < n; i++) {
        this.views[i].aim(i * (2 * Math.PI / n), tilt);
      }
    } else {
      this.views[0].aim(0, 0);
    }
  };

  Renderer.prototype.draw = function () {
    var gl = this.gl, v = this.video;
    if (!v.videoWidth || !v.videoHeight) return false;

    var w = this.canvas.clientWidth | 0, h = this.canvas.clientHeight | 0;
    if (!w || !h) return false;
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var cw = (w * dpr) | 0, ch = (h * dpr) | 0;
    if (this.canvas.width !== cw || this.canvas.height !== ch) {
      this.canvas.width = cw;
      this.canvas.height = ch;
    }

    gl.bindTexture(gl.TEXTURE_2D, this.tex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGB, gl.RGB, gl.UNSIGNED_BYTE, v);

    gl.uniform1i(this.u.u_tex, 0);
    gl.uniform2f(this.u.u_center, this.center[0], this.center[1]);
    gl.uniform1f(this.u.u_radius, this.radius);
    gl.uniform1f(this.u.u_halfFov, this.halfFov);
    gl.uniform1f(this.u.u_flip, this.flip);
    gl.uniform1f(this.u.u_proj, this.projection);
    gl.uniform1f(this.u.u_ceiling, this.ceiling ? 1 : 0);

    // Panes tile the canvas: one fills it, two stack, four go 2x2.
    var n = this.views.length;
    var cols = n >= 4 ? 2 : 1, rows = Math.ceil(n / cols);
    var pw = Math.floor(cw / cols), ph = Math.floor(ch / rows);
    gl.enable(gl.SCISSOR_TEST);
    for (var i = 0; i < n; i++) {
      var col = i % cols, row = (i / cols) | 0;
      var x = col * pw, y = ch - (row + 1) * ph;
      gl.viewport(x, y, pw, ph);
      gl.scissor(x, y, pw, ph);
      var view = this.views[i];
      gl.uniform1f(this.u.u_yaw, view.yaw);
      gl.uniform1f(this.u.u_pitch, view.pitch);
      gl.uniform1f(this.u.u_zoom, view.zoom);
      gl.uniform1f(this.u.u_aspect, pw / ph);
      gl.drawArrays(gl.TRIANGLES, 0, 6);
    }
    gl.disable(gl.SCISSOR_TEST);
    return true;
  };

  // paneAt maps a point on the canvas to the pane under it, so a drag
  // moves the pane it started in and no other.
  Renderer.prototype.paneAt = function (px, py) {
    var n = this.views.length;
    var cols = n >= 4 ? 2 : 1, rows = Math.ceil(n / cols);
    var w = this.canvas.clientWidth, h = this.canvas.clientHeight;
    var col = Math.min(cols - 1, Math.max(0, Math.floor(px / (w / cols))));
    var row = Math.min(rows - 1, Math.max(0, Math.floor(py / (h / rows))));
    return this.views[row * cols + col] || this.views[0];
  };

  window.ReoFisheye = { Renderer: Renderer, View: View };
})();
