(function () {
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
    syncMediaPanel(panelFromEvent(e));
  });

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      syncMediaPanel(document.getElementById('media-panel'));
    });
  } else {
    syncMediaPanel(document.getElementById('media-panel'));
  }
})();
