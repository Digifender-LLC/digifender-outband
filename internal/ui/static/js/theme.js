(function () {
  "use strict";

  var STORAGE_KEY = "mipmi-theme";

  function systemTheme() {
    try {
      return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
    } catch (e) {
      return "dark";
    }
  }

  function readTheme() {
    try {
      var t = localStorage.getItem(STORAGE_KEY);
      if (t === "light" || t === "dark") return t;
    } catch (e) { /* ignore */ }
    return systemTheme();
  }

  function applyTheme(theme) {
    if (theme !== "light" && theme !== "dark") theme = "dark";
    document.documentElement.setAttribute("data-theme", theme);
    try {
      localStorage.setItem(STORAGE_KEY, theme);
    } catch (e) { /* ignore */ }
    syncToggles(theme);
    try {
      window.dispatchEvent(new CustomEvent("mipmi-theme", { detail: { theme: theme } }));
    } catch (e) { /* ignore */ }
  }

  function syncToggles(theme) {
    var label = theme === "light" ? "Dark mode" : "Light mode";
    document.querySelectorAll("[data-theme-toggle]").forEach(function (btn) {
      btn.setAttribute("aria-pressed", theme === "dark" ? "true" : "false");
      btn.textContent = label;
      btn.title = "Switch to " + (theme === "light" ? "dark" : "light") + " theme";
    });
  }

  function toggleTheme() {
    var cur = document.documentElement.getAttribute("data-theme") || readTheme();
    applyTheme(cur === "light" ? "dark" : "light");
  }

  function setDrawer(open) {
    document.body.classList.toggle("drawer-open", open);
    document.querySelectorAll("[data-drawer-toggle]").forEach(function (btn) {
      btn.setAttribute("aria-expanded", open ? "true" : "false");
      btn.setAttribute("aria-label", open ? "Close navigation" : "Open navigation");
    });
  }

  function onReady() {
    applyTheme(readTheme());

    document.addEventListener("click", function (ev) {
      var t = ev.target;
      if (!t || !t.closest) return;
      if (t.closest("[data-theme-toggle]")) {
        toggleTheme();
        return;
      }
      if (t.closest("[data-drawer-toggle]")) {
        setDrawer(!document.body.classList.contains("drawer-open"));
        return;
      }
      if (t.closest("[data-drawer-close]")) {
        setDrawer(false);
        return;
      }
      // Close drawer when a nav link is followed on mobile.
      if (document.body.classList.contains("drawer-open") && t.closest(".sidebar-nav a")) {
        setDrawer(false);
      }
    });

    window.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape") setDrawer(false);
    });

    window.addEventListener("storage", function (ev) {
      if (ev.key === STORAGE_KEY && (ev.newValue === "light" || ev.newValue === "dark")) {
        applyTheme(ev.newValue);
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", onReady);
  } else {
    onReady();
  }
})();
