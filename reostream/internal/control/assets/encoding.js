// encoding.js: keep the encoder form offering only what the camera takes.
//
// The stops are not independent. A Reolink camera answers GetEnc's range
// with one whole combination per resolution, and the bitrates inside each
// belong to that resolution alone: the pano offers 12288 kbps at 7680x2160
// and refuses it at 4096x1152. So changing the resolution has to rewrite
// the lists beside it, or the form would offer a pair the camera will not
// take and the only sign would be a refusal after the fact.
//
// Without this the page still works: it was rendered with the stops for the
// resolution in force, and the server checks every value against the same
// table the camera gave it. This only stops a wrong pair being offered.

(function () {
  'use strict';

  var root = document.getElementById('encoding');
  if (!root) return;

  var options;
  try {
    options = JSON.parse(root.getAttribute('data-options') || '[]');
  } catch (e) {
    return; // leave the rendered stops alone rather than empty the form
  }
  if (!options.length) return;

  var size = document.getElementById('enc-main-size');
  if (!size) return;

  // What the form held when the page was drawn, for Revert. Captured before
  // anything can change it.
  var opened = {};
  var fields = ['enc-main-size', 'enc-main-bitrate', 'enc-main-framerate',
                'enc-main-gop', 'enc-sub-bitrate', 'enc-sub-framerate', 'enc-sub-gop'];
  fields.forEach(function (id) {
    var el = document.getElementById(id);
    if (el) opened[id] = el.value;
  });

  var note = document.getElementById('enc-note');
  function say(msg) {
    if (note) note.textContent = msg;
  }

  function comboFor(sz) {
    for (var i = 0; i < options.length; i++) {
      if (options[i].main && options[i].main.size === sz) return options[i];
    }
    return null;
  }

  // Refill a <select> with values, keeping what was chosen if the new list
  // still has it. Keeping it matters: changing resolution and back should
  // not quietly move the bitrate somewhere else.
  function refill(id, values, suffix) {
    var el = document.getElementById(id);
    if (!el || !values) return;
    var had = el.value;
    el.textContent = '';
    values.forEach(function (v) {
      var opt = document.createElement('option');
      opt.value = String(v);
      opt.textContent = String(v) + suffix;
      el.appendChild(opt);
    });
    if (values.indexOf(Number(had)) !== -1) {
      el.value = had;
    } else {
      // Fall to the highest offered rather than the first: the lists arrive
      // ascending for bitrate and descending for frame rate, so "the first"
      // would mean the worst picture in one and the best in the other.
      el.value = String(Math.max.apply(null, values));
    }
    return had !== el.value;
  }

  function bound(id, min, max) {
    var el = document.getElementById(id);
    if (!el || min === undefined) return;
    el.min = String(min);
    el.max = String(max);
    var n = Number(el.value);
    if (n < min) el.value = String(min);
    if (n > max) el.value = String(max);
  }

  function applySize() {
    var combo = comboFor(size.value);
    if (!combo) return;
    var moved = refill('enc-main-bitrate', combo.main.bitRates, ' kbps');
    var movedFps = refill('enc-main-framerate', combo.main.frameRates, ' fps');
    bound('enc-main-gop', combo.main.gopMin, combo.main.gopMax);
    if (combo.sub) {
      refill('enc-sub-bitrate', combo.sub.bitRates, ' kbps');
      refill('enc-sub-framerate', combo.sub.frameRates, ' fps');
      bound('enc-sub-gop', combo.sub.gopMin, combo.sub.gopMax);
    }
    if (moved || movedFps) {
      say(size.value + ' does not offer what was selected, so it moved to the highest this camera takes.');
    } else {
      say('');
    }
  }

  size.addEventListener('change', applySize);

  var revert = document.getElementById('enc-revert');
  if (revert) {
    revert.addEventListener('click', function () {
      // Size first, so the lists are the right ones before the values that
      // belong to them are put back.
      if (opened['enc-main-size'] !== undefined) size.value = opened['enc-main-size'];
      applySize();
      fields.forEach(function (id) {
        var el = document.getElementById(id);
        if (el && opened[id] !== undefined) el.value = opened[id];
      });
      say('back to the values this page opened with — press Set encoding to send it');
    });
  }
})();
