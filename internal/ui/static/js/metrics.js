(function () {
  "use strict";

  var SERIES_PALETTE = [
    "#3dba9c",
    "#5b8def",
    "#e6a23c",
    "#c45d8c",
    "#7bc96f",
    "#9b7bde",
    "#e07050",
    "#4db6c8",
    "#d4a017",
    "#6fbf73",
  ];

  function qs(sel, root) {
    return (root || document).querySelector(sel);
  }

  function qsa(sel, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(sel));
  }

  function cssVar(name, fallback) {
    var v = getComputedStyle(document.documentElement).getPropertyValue(name);
    v = (v || "").trim();
    return v || fallback;
  }

  function themeColors() {
    return {
      text: cssVar("--text", "#e6ebf2"),
      muted: cssVar("--muted", "#8b97a8"),
      border: cssVar("--border", "#2a3340"),
      surface: cssVar("--surface", "#161b22"),
      bg: cssVar("--bg", "#0e1116"),
    };
  }

  function hexAlpha(hex, alpha) {
    var h = (hex || "").replace("#", "");
    if (h.length === 3) {
      h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2];
    }
    if (h.length !== 6) return hex;
    var r = parseInt(h.slice(0, 2), 16);
    var g = parseInt(h.slice(2, 4), 16);
    var b = parseInt(h.slice(4, 6), 16);
    return "rgba(" + r + "," + g + "," + b + "," + alpha + ")";
  }

  function bootstrap() {
    var el = qs("#metrics-bootstrap");
    if (!el) return { range: "1h", sensors: [] };
    try {
      return JSON.parse(el.textContent);
    } catch (e) {
      return { range: "1h", sensors: [] };
    }
  }

  function selectedSensors() {
    return qsa('#metrics-form input[name="sensor"]:checked').map(function (el) {
      return el.value;
    });
  }

  function buildURL(cfg) {
    var u = new URL("api/metrics", window.location.href);
    u.searchParams.set("range", cfg.range || "1h");
    (cfg.sensors || []).forEach(function (s) {
      u.searchParams.append("sensor", s);
    });
    return u.toString();
  }

  function pad2(n) {
    return n < 10 ? "0" + n : String(n);
  }

  function formatTick(ms, spanMs) {
    var d = new Date(ms);
    if (spanMs > 36 * 3600 * 1000) {
      var months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
      return months[d.getMonth()] + " " + d.getDate();
    }
    return pad2(d.getHours()) + ":" + pad2(d.getMinutes());
  }

  function formatTooltipTime(ms) {
    var d = new Date(ms);
    return (
      d.getFullYear() +
      "-" +
      pad2(d.getMonth() + 1) +
      "-" +
      pad2(d.getDate()) +
      " " +
      pad2(d.getHours()) +
      ":" +
      pad2(d.getMinutes()) +
      ":" +
      pad2(d.getSeconds())
    );
  }

  function seriesToDatasets(series) {
    return series.map(function (s, i) {
      var color = SERIES_PALETTE[i % SERIES_PALETTE.length];
      return {
        label: s.sensor + (s.unit ? " (" + s.unit + ")" : ""),
        data: (s.points || []).map(function (p) {
          return { x: p.ts * 1000, y: p.value };
        }),
        borderColor: color,
        backgroundColor: hexAlpha(color, 0.12),
        borderWidth: 2,
        tension: 0.25,
        spanGaps: true,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointHitRadius: 8,
        fill: false,
      };
    });
  }

  function chartOptions(spanMs) {
    var t = themeColors();
    return {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      interaction: {
        mode: "nearest",
        axis: "x",
        intersect: false,
      },
      plugins: {
        legend: {
          position: "bottom",
          labels: {
            color: t.muted,
            boxWidth: 12,
            boxHeight: 12,
            padding: 16,
            usePointStyle: true,
            pointStyle: "line",
          },
        },
        tooltip: {
          backgroundColor: t.surface,
          titleColor: t.text,
          bodyColor: t.text,
          borderColor: t.border,
          borderWidth: 1,
          displayColors: true,
          callbacks: {
            title: function (items) {
              if (!items.length) return "";
              return formatTooltipTime(items[0].parsed.x);
            },
          },
        },
      },
      scales: {
        x: {
          type: "linear",
          bounds: "data",
          ticks: {
            color: t.muted,
            maxRotation: 0,
            autoSkip: true,
            maxTicksLimit: 8,
            callback: function (value) {
              return formatTick(value, spanMs);
            },
          },
          grid: {
            color: t.border,
            drawBorder: false,
          },
          border: { display: false },
        },
        y: {
          ticks: {
            color: t.muted,
          },
          grid: {
            color: t.border,
            drawBorder: false,
          },
          border: { display: false },
        },
      },
    };
  }

  function dataSpanMs(series) {
    var min = Infinity;
    var max = -Infinity;
    series.forEach(function (s) {
      (s.points || []).forEach(function (p) {
        var ms = p.ts * 1000;
        if (ms < min) min = ms;
        if (ms > max) max = ms;
      });
    });
    if (!isFinite(min) || !isFinite(max) || max <= min) return 3600 * 1000;
    return max - min;
  }

  var chart = null;
  var lastPayload = null;

  function destroyChart() {
    if (chart) {
      chart.destroy();
      chart = null;
    }
  }

  function ensureCanvas(host) {
    var canvas = host.querySelector("canvas");
    if (!canvas) {
      host.innerHTML = "";
      canvas = document.createElement("canvas");
      canvas.setAttribute("aria-label", "Sensor history chart");
      host.appendChild(canvas);
    }
    return canvas;
  }

  function syncPickerUI() {
    var chips = qs("#metrics-chips");
    var count = qs("#metrics-count");
    var selected = selectedSensors();
    if (count) count.textContent = String(selected.length);
    if (!chips) return;

    chips.innerHTML = "";
    if (!selected.length) {
      var empty = document.createElement("span");
      empty.className = "muted metrics-chips-empty";
      empty.textContent = "None selected — pick sensors below.";
      chips.appendChild(empty);
      return;
    }

    selected.forEach(function (name) {
      var chipEl = document.createElement("span");
      chipEl.className = "chip";
      chipEl.title = name;

      var label = document.createElement("span");
      label.className = "chip-label";
      label.textContent = name;

      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "chip-remove";
      btn.setAttribute("aria-label", "Remove " + name);
      btn.textContent = "×";
      btn.addEventListener("click", function () {
        var input = qsa('#metrics-form input[name="sensor"]').find(function (el) {
          return el.value === name;
        });
        if (input) {
          input.checked = false;
          syncPickerUI();
        }
      });

      chipEl.appendChild(label);
      chipEl.appendChild(btn);
      chips.appendChild(chipEl);
    });
  }

  function applyFilter() {
    var filterEl = qs("#metrics-filter");
    var emptyEl = qs("#metrics-filter-empty");
    var q = (filterEl && filterEl.value ? filterEl.value : "")
      .trim()
      .toLowerCase();
    var visible = 0;
    qsa(".sensor-item").forEach(function (item) {
      var name = (item.getAttribute("data-name") || "").toLowerCase();
      var show = !q || name.indexOf(q) !== -1;
      item.hidden = !show;
      if (show) visible += 1;
    });
    if (emptyEl) emptyEl.hidden = visible > 0 || !qs("#sensor-list");
  }

  function render(payload) {
    var host = qs("#metrics-chart");
    var status = qs("#metrics-status");
    if (!host || typeof Chart === "undefined") return;

    lastPayload = payload;
    var series = payload.series || [];
    var total = series.reduce(function (n, s) {
      return n + (s.points ? s.points.length : 0);
    }, 0);
    if (status) {
      var warm = payload.meta && payload.meta.warm;
      status.textContent = total
        ? total + " samples · " + series.length + " series"
        : warm
          ? "No samples in this window yet."
          : "Collector warming…";
    }
    if (!total) {
      destroyChart();
      host.innerHTML = "";
      return;
    }

    var spanMs = dataSpanMs(series);
    var datasets = seriesToDatasets(series);
    destroyChart();
    host.innerHTML = "";
    var canvas = ensureCanvas(host);

    chart = new Chart(canvas.getContext("2d"), {
      type: "line",
      data: { datasets: datasets },
      options: chartOptions(spanMs),
    });
  }

  function applyThemeToChart() {
    if (!lastPayload) return;
    destroyChart();
    var host = qs("#metrics-chart");
    if (host) host.innerHTML = "";
    render(lastPayload);
  }

  function currentConfig() {
    var cfg = bootstrap();
    var rangeEl = qs("#metrics-range");
    if (rangeEl) cfg.range = rangeEl.value;
    cfg.sensors = selectedSensors();
    return cfg;
  }

  function load() {
    var cfg = currentConfig();
    fetch(buildURL(cfg), { credentials: "same-origin" })
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.json();
      })
      .then(render)
      .catch(function (err) {
        var status = qs("#metrics-status");
        if (status) status.textContent = "Failed to load metrics: " + err.message;
      });
  }

  function syncURL(cfg) {
    try {
      var u = new URL(window.location.href);
      u.searchParams.delete("sensor");
      u.searchParams.delete("range");
      u.searchParams.set("range", cfg.range || "1h");
      (cfg.sensors || []).forEach(function (s) {
        u.searchParams.append("sensor", s);
      });
      history.replaceState(null, "", u.pathname + u.search);
    } catch (e) {
      /* ignore */
    }
  }

  function onReady() {
    var form = qs("#metrics-form");
    syncPickerUI();
    applyFilter();
    load();

    if (form) {
      form.addEventListener("submit", function (ev) {
        ev.preventDefault();
        var cfg = currentConfig();
        syncURL(cfg);
        load();
      });
      form.addEventListener("change", function (ev) {
        var t = ev.target;
        if (!t) return;
        if (t.name === "sensor") {
          syncPickerUI();
        }
        if (t.id === "metrics-range") {
          load();
        }
      });
    }

    var filterEl = qs("#metrics-filter");
    if (filterEl) {
      filterEl.addEventListener("input", applyFilter);
    }

    var clearBtn = qs("#metrics-clear");
    if (clearBtn) {
      clearBtn.addEventListener("click", function () {
        qsa('#metrics-form input[name="sensor"]').forEach(function (el) {
          el.checked = false;
        });
        syncPickerUI();
      });
    }

    window.addEventListener("resize", function () {
      if (chart) chart.resize();
    });
    window.addEventListener("outband-theme", function () {
      applyThemeToChart();
    });
    setInterval(load, 15000);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", onReady);
  } else {
    onReady();
  }
})();
