(function () {
  const banner = document.getElementById("sol-banner");
  const el = document.getElementById("terminal");
  if (!el || typeof Terminal === "undefined") {
    if (banner) {
      banner.textContent = "xterm.js failed to load";
      banner.className = "sol-banner err";
    }
    return;
  }

  const term = new Terminal({
    cursorBlink: true,
    fontFamily: '"IBM Plex Mono", "Source Code Pro", Consolas, monospace',
    fontSize: 14,
    theme: {
      background: "#0b0d10",
      foreground: "#d7dde8",
      cursor: "#3dba9c",
      selectionBackground: "#2a3a44",
    },
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(el);
  fitAddon.fit();
  window.addEventListener("resize", () => fitAddon.fit());

  function setBanner(text, cls) {
    if (!banner) return;
    banner.textContent = text;
    banner.className = "sol-banner" + (cls ? " " + cls : "");
  }

  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(proto + "//" + location.host + "/ws/sol");
  ws.binaryType = "arraybuffer";

  ws.onopen = function () {
    setBanner("Connected — serial console active", "ok");
  };
  ws.onclose = function () {
    setBanner("Disconnected — refresh to reconnect (single SOL session)", "warn");
  };
  ws.onerror = function () {
    setBanner("WebSocket error", "err");
  };
  ws.onmessage = function (ev) {
    if (typeof ev.data === "string") {
      term.write(ev.data);
      return;
    }
    term.write(new Uint8Array(ev.data));
  };

  term.onData(function (data) {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(data);
    }
  });
})();
