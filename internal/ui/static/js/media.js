(function () {
  var pollTimer;

  function syncMediaPanel(panel) {
    if (!panel) return;
    var checked = panel.querySelector('input[name=source]:checked');
    var mode = checked ? checked.value : 'library';
    panel.dataset.source = mode;

    panel.querySelectorAll('.media-library-only').forEach(function (el) {
      el.hidden = mode !== 'library';
    });
    panel.querySelectorAll('.media-url-only').forEach(function (el) {
      el.hidden = mode !== 'url';
    });

    var iso = panel.querySelector('.media-mount [name=iso]');
    var url = panel.querySelector('.media-mount [name=url]');
    if (iso) iso.required = mode === 'library' && !iso.disabled;
    if (url) url.required = mode === 'url';
  }

  function stopMediaPoll() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function startMediaPoll(panel) {
    stopMediaPoll();
    if (!panel || panel.dataset.poll !== '1' || !panel.dataset.pollUrl) return;
    pollTimer = setInterval(function () {
      if (typeof htmx === 'undefined') return;
      htmx.ajax('GET', panel.dataset.pollUrl, {
        target: '#media-panel',
        swap: 'outerHTML',
      });
    }, 1000);
  }

  function panelFromEvent(e) {
    if (e.target && e.target.id === 'media-panel') return e.target;
    if (e.target && e.target.querySelector) {
      return e.target.querySelector('#media-panel');
    }
    return document.getElementById('media-panel');
  }

  document.body.addEventListener('change', function (e) {
    if (!e.target.matches('#media-panel input[name=source]')) return;
    syncMediaPanel(document.getElementById('media-panel'));
  });

  document.body.addEventListener('htmx:afterSwap', function (e) {
    var panel = panelFromEvent(e);
    syncMediaPanel(panel);
    startMediaPoll(panel);
  });

  document.body.addEventListener('htmx:beforeRequest', function (e) {
    if (e.target.closest && e.target.closest('#media-panel form')) {
      stopMediaPoll();
    }
  });

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      var panel = document.getElementById('media-panel');
      syncMediaPanel(panel);
      startMediaPoll(panel);
    });
  } else {
    var panel = document.getElementById('media-panel');
    syncMediaPanel(panel);
    startMediaPoll(panel);
  }
})();
