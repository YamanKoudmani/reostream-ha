// Every player on either page is built here, so "live" means the same
// thing everywhere.
//
// Left alone, mpegts.js treats a live stream like a recording: pause for a
// minute and play again and it resumes a minute behind, and stays there.
// For a camera that is not playback, it is a clock that is wrong. The
// latency chasing options below keep it at the edge, and seeking to the
// end of the buffer on play covers the case where it has already drifted
// before those take effect.
window.reostreamPlayer = function (url, video) {
  // No liveBufferLatencyChasing. It keeps a timer polling the SourceBuffer,
  // and a player that has been destroyed leaves that timer behind: one page
  // load measured 7,597 exceptions reading "buffered" off a SourceBuffer
  // removed from its media source, which is what made the page crawl. The
  // seek below does the same job at the only moment it matters.
  var player = mpegts.createPlayer({type: 'mpegts', isLive: true, url: url});
  if (video) {
    video.addEventListener('play', function () {
      try {
        var b = video.buffered;
        if (b && b.length) {
          var edge = b.end(b.length - 1);
          if (edge - video.currentTime > 1) video.currentTime = edge - 0.1;
        }
      } catch (e) {}
    });
  }
  return player;
};


// Snapshots. The tile's <img> starts empty because the keyframe endpoint
// serves a raw elementary stream no browser can render; the decoder that
// can is the one already on this page, so a frame is decoded here and
// drawn into the tag.
//
// Strictly one at a time, and the connection is dropped the moment a frame
// has been drawn. Eight tiles opening eight streams at once is exactly what
// the play button was built to avoid, and a snapshot must not reintroduce
// it by the back door.
window.reostreamSnapshots = function (containers) {
  if (!window.mpegts || !mpegts.isSupported()) return;
  var queue = Array.prototype.slice.call(containers).filter(function (c) {
    // A tile that plays by itself needs no snapshot: it shows live video,
    // and it keeps its own last frame when it stops. Running both meant
    // two players for one container, fighting over one video element.
    if (c.dataset.autoplay) return false;
    return c.dataset.src && c.querySelector('img') && c.querySelector('video');
  });

  function next() {
    var container = queue.shift();
    if (!container) return;
    var video = container.querySelector('video');
    var img = container.querySelector('img');
    var player = reostreamPlayer(container.dataset.src, video);
    var done = false;

    function finish(drew) {
      if (done) return;
      done = true;
      clearTimeout(timer);
      video.removeEventListener('loadeddata', draw);
      try { player.pause(); } catch (e) {}
      try { player.unload(); } catch (e) {}
      try { player.detachMediaElement(); } catch (e) {}
      try { player.destroy(); } catch (e) {}
      video.hidden = true;
      if (drew) {
        img.hidden = false;
      }
      next();
    }

    function draw() {
      if (!video.videoWidth || !video.videoHeight) return finish(false);
      try {
        var canvas = document.createElement('canvas');
        canvas.width = video.videoWidth;
        canvas.height = video.videoHeight;
        canvas.getContext('2d').drawImage(video, 0, 0);
        img.src = canvas.toDataURL('image/jpeg', 0.7);
        finish(true);
      } catch (e) {
        finish(false);
      }
    }

    // A camera that is configured but not delivering must not stall the
    // queue behind it.
    var timer = setTimeout(function () { finish(false); }, 8000);

    video.addEventListener('loadeddata', draw);
    try {
      player.attachMediaElement(video);
      player.load();
      // Muted and hidden: this is never shown playing, it exists to put
      // one frame on a canvas.
      var p = video.play();
      if (p && p.catch) p.catch(function () {});
    } catch (e) {
      finish(false);
    }
  }

  next();
};


// Sound: off unless this browser turned it on before. Every player follows
// the speaker button, so the choice made on one camera holds on the next.
window.reostreamAudio = {
  wanted: function () {
    try { return localStorage.getItem('reostream.audio') === 'on'; } catch (e) { return false; }
  },
  // Start video with the remembered sound setting and keep the setting in
  // step with the speaker button. A browser that refuses to start with sound
  // (no tap yet) gets the picture muted rather than nothing at all.
  play: function (video) {
    var self = this;
    if (!video.dataset.audioFollow) {
      video.dataset.audioFollow = '1';
      video.addEventListener('volumechange', function () {
        // A change this code made is not the viewer's choice.
        if (video._reoSkip) { video._reoSkip = false; return; }
        try { localStorage.setItem('reostream.audio', video.muted || video.volume === 0 ? 'off' : 'on'); } catch (e) {}
      });
    }
    var setMuted = function (m) {
      if (video.muted === m) return;
      video._reoSkip = true;
      video.muted = m;
    };
    setMuted(!self.wanted());
    var p = video.play();
    if (p && p.catch) {
      p.catch(function () {
        if (video.muted) return;
        setMuted(true);
        var q = video.play();
        if (q && q.catch) q.catch(function () {});
      });
    }
  }
};
