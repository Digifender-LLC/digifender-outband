(function () {
  var pollTimer;

  function syncMediaPanel(panel) {
    if (!panel) return;
    var mountForm = panel.querySelector('.media-mount');
    if (!mountForm) return;
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

  function hideUploadProgress(panel) {
    if (!panel) return;
    var box = panel.querySelector('#media-upload-progress');
    if (box) box.hidden = true;
  }

  function showUploadProgress(panel, loaded, total) {
    if (!panel) return;
    var box = panel.querySelector('#media-upload-progress');
    var bar = box && box.querySelector('progress');
    var text = box && box.querySelector('.media-upload-progress-text');
    if (!box || !bar || !text) return;
    box.hidden = false;
    if (total > 0) {
      bar.max = total;
      bar.value = loaded;
      text.textContent = formatBytes(loaded) + ' / ' + formatBytes(total);
    } else {
      bar.removeAttribute('max');
      bar.value = 0;
      text.textContent = formatBytes(loaded) + ' uploaded';
    }
  }

  function formatBytes(n) {
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KiB';
    if (n < 1024 * 1024 * 1024) return (n / (1024 * 1024)).toFixed(1) + ' MiB';
    return (n / (1024 * 1024 * 1024)).toFixed(2) + ' GiB';
  }

  function uploadWithProgress(form, panel) {
    var fd = new FormData(form);
    var xhr = new XMLHttpRequest();
    xhr.open('POST', form.action);
    xhr.upload.onprogress = function (ev) {
      showUploadProgress(panel, ev.loaded, ev.total || 0);
    };
    xhr.onload = function () {
      hideUploadProgress(panel);
      if (xhr.status >= 200 && xhr.status < 300) {
        if (typeof htmx !== 'undefined') {
          htmx.swap(panel, xhr.responseText, { swapStyle: 'outerHTML' });
        } else {
          panel.outerHTML = xhr.responseText;
        }
        startMediaPoll(document.getElementById('media-panel'));
      } else if (typeof htmx !== 'undefined') {
        htmx.swap(panel, xhr.responseText, { swapStyle: 'outerHTML' });
      }
    };
    xhr.onerror = function () {
      hideUploadProgress(panel);
    };
    xhr.send(fd);
  }

  function panelFromEvent(e) {
    if (e.target && e.target.id === 'media-panel') return e.target;
    if (e.target && e.target.querySelector) {
      return e.target.querySelector('#media-panel');
    }
    return document.getElementById('media-panel');
  }

  document.body.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form.classList || !form.classList.contains('media-upload')) return;
    var panel = form.closest('#media-panel');
    if (!panel) return;
    e.preventDefault();
    stopMediaPoll();
    uploadWithProgress(form, panel);
  });

  document.body.addEventListener('htmx:afterSwap', function (e) {
    var panel = panelFromEvent(e);
    syncMediaPanel(panel);
    startMediaPoll(panel);
  });

  document.body.addEventListener('htmx:beforeRequest', function (e) {
    if (e.target.closest && e.target.closest('#media-panel form:not(.media-upload)')) {
      stopMediaPoll();
    }
  });

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initMediaPanel);
  } else {
    initMediaPanel();
  }

  function initMediaPanel() {
    var panel = document.getElementById('media-panel');
    syncMediaPanel(panel);
    startMediaPoll(panel);
  }
})();
