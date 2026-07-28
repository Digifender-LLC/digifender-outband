(function () {
  "use strict";

  function qs(sel) {
    return document.querySelector(sel);
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

  function buildURL(cfg) {
    var u = new URL("/api/metrics", window.location.origin);
    u.searchParams.set("range", cfg.range || "1h");
    (cfg.sensors || []).forEach(function (s) {
      u.searchParams.append("sensor", s);
    });
    return u.toString();
  }

  function toUPlot(series) {
    var times = {};
    series.forEach(function (s) {
      (s.points || []).forEach(function (p) {
        times[p.ts] = true;
      });
    });
    var xs = Object.keys(times)
      .map(Number)
      .sort(function (a, b) {
        return a - b;
      });
    var data = [xs];
    series.forEach(function (s) {
      var map = {};
      (s.points || []).forEach(function (p) {
        map[p.ts] = p.value;
      });
      data.push(
        xs.map(function (t) {
          return map[t] != null ? map[t] : null;
        })
      );
    });
    return data;
  }

  var plot = null;

  function render(payload) {
    var host = qs("#metrics-chart");
    var status = qs("#metrics-status");
    if (!host || typeof uPlot === "undefined") return;

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
      if (plot) {
        plot.destroy();
        plot = null;
      }
      host.innerHTML = "";
      return;
    }

    var data = toUPlot(series);
    var opts = {
      width: host.clientWidth || 800,
      height: 360,
      series: [{ label: "Time" }].concat(
        series.map(function (s) {
          return {
            label: s.sensor + (s.unit ? " (" + s.unit + ")" : ""),
            stroke: undefined,
            width: 1.5,
            spanGaps: true,
          };
        })
      ),
      axes: [
        {
          stroke: "#8b97a8",
          grid: { stroke: "#2e3744" },
          ticks: { stroke: "#2e3744" },
        },
        {
          stroke: "#8b97a8",
          grid: { stroke: "#2e3744" },
          ticks: { stroke: "#2e3744" },
        },
      ],
      scales: { x: { time: true } },
      legend: { show: true },
    };

    if (plot) {
      plot.destroy();
      plot = null;
    }
    host.innerHTML = "";
    plot = new uPlot(opts, data, host);
  }

  function load() {
    var cfg = bootstrap();
    var rangeEl = qs("#metrics-range");
    if (rangeEl) cfg.range = rangeEl.value;
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

  function onReady() {
    load();
    window.addEventListener("resize", function () {
      if (plot) {
        var host = qs("#metrics-chart");
        if (host) plot.setSize({ width: host.clientWidth || 800, height: 360 });
      }
    });
    setInterval(load, 15000);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", onReady);
  } else {
    onReady();
  }
})();
