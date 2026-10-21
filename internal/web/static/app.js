(() => {
  "use strict";

  const OP = {
    OUTPUT: 0x01,
    INPUT: 0x02,
    RESIZE: 0x03,
    SNAPSHOT: 0x04,
    CREATE: 0x10,
    CLOSE: 0x11,
    ATTACHED: 0x12,
    EXIT: 0x13,
    ERROR: 0x14,
    RENAME: 0x15,
    NAME: 0x16,
  };
  const HEADER = 3;
  const encoder = new TextEncoder();
  const decoder = new TextDecoder();

  const statusEl = document.getElementById("status");
  const agentEl = document.getElementById("agent");
  const termEl = document.getElementById("term");
  const sessionWrap = document.getElementById("session");
  const sessionNameEl = document.getElementById("session-name");
  const sessionInput = document.getElementById("session-input");
  const sessionSave = document.getElementById("session-save");
  const sessionCancel = document.getElementById("session-cancel");

  const term = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily:
      '"JetBrainsMonoNL Nerd Font Mono", "JetBrainsMono Nerd Font Mono", "JetBrainsMono Nerd Font", Menlo, Monaco, "Cascadia Code", monospace',
    fontWeight: "400",
    fontWeightBold: "700",
    lineHeight: 1.2,
    letterSpacing: 0,
    scrollback: 5000,
    theme: {
      background: "#0f1115",
      foreground: "#d7dae0",
      cursor: "#7aa2f7",
      selectionBackground: "#2a2e37",
    },
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(termEl);

  const params = new URLSearchParams(location.search);
  const relay = params.get("relay") || "";
  const relayServer = params.get("server") || "";
  const relayTLS = params.get("tls");
  const scope = relay ? relayServer || "relay" : "local";

  let currentAgent =
    params.get("agent") || localStorage.getItem("agentty.agent") || "";
  let sessionId = "";
  let sessionName = "";
  let ws = null;
  let attempt = 0;
  let reconnectTimer = null;

  function wsTarget() {
    if (relay) {
      let scheme;
      if (relayTLS === "1") {
        scheme = "wss:";
      } else if (relayTLS === "0") {
        scheme = "ws:";
      } else if (location.protocol === "https:" || relay.endsWith(":443")) {
        scheme = "wss:";
      } else {
        scheme = "ws:";
      }
      const query = new URLSearchParams({
        serverId: relayServer,
        role: "client",
        v: "2",
      });
      return `${scheme}//${relay}/ws?${query.toString()}`;
    }
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    return `${scheme}//${location.host}/ws`;
  }

  function setStatus(text, kind) {
    statusEl.textContent = text;
    statusEl.className = kind || "";
  }

  function updateSessionLabel() {
    const label = sessionName || sessionId;
    if (!label) return;
    sessionNameEl.textContent = label;
    sessionWrap.hidden = false;
  }

  function frame(op, slot, payload) {
    const bytes =
      payload instanceof Uint8Array ? payload : new Uint8Array(payload || 0);
    const out = new Uint8Array(HEADER + bytes.length);
    out[0] = op;
    out[1] = (slot >> 8) & 0xff;
    out[2] = slot & 0xff;
    out.set(bytes, HEADER);
    return out;
  }

  function resizePayload(rows, cols) {
    const out = new Uint8Array(4);
    out[0] = (rows >> 8) & 0xff;
    out[1] = rows & 0xff;
    out[2] = (cols >> 8) & 0xff;
    out[3] = cols & 0xff;
    return out;
  }

  function sessionKey() {
    return `agentty.session.${scope}`;
  }

  function loadSession() {
    sessionId = localStorage.getItem(sessionKey()) || "";
  }

  function generateSessionID() {
    const bytes = new Uint8Array(8);
    if (window.crypto && window.crypto.getRandomValues) {
      window.crypto.getRandomValues(bytes);
    } else {
      for (let i = 0; i < bytes.length; i += 1) {
        bytes[i] = Math.floor(Math.random() * 256);
      }
    }
    return (
      "sess_" +
      Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")
    );
  }

  function sendCreate() {
    if (!sessionId) {
      sessionId = generateSessionID();
      localStorage.setItem(sessionKey(), sessionId);
    }
    const request = {
      slot: 0,
      session: sessionId,
      agent: currentAgent,
      cols: term.cols,
      rows: term.rows,
    };
    ws.send(frame(OP.CREATE, 0, encoder.encode(JSON.stringify(request))));
  }

  let wsGeneration = 0;

  function connect() {
    const generation = ++wsGeneration;
    if (ws) {
      try {
        ws.close();
      } catch (_) {}
    }
    clearTimeout(reconnectTimer);
    setStatus(relay ? "connecting via relay…" : "connecting…");
    const socket = new WebSocket(wsTarget());
    ws = socket;
    socket.binaryType = "arraybuffer";

    socket.onopen = () => {
      if (generation !== wsGeneration) return;
      attempt = 0;
      sessionName = "";
      sessionWrap.hidden = true;
      setStatus(relay ? "connected via relay" : "connected", "ok");
      loadSession();
      refit();
      sendCreate();
    };

    socket.onmessage = (event) => {
      if (generation !== wsGeneration) return;
      const bytes = new Uint8Array(event.data);
      if (bytes.length < HEADER) return;
      const op = bytes[0];
      const payload = bytes.subarray(HEADER);

      switch (op) {
        case OP.OUTPUT:
          term.write(payload);
          break;
        case OP.SNAPSHOT:
          term.reset();
          term.write(payload);
          break;
        case OP.ATTACHED: {
          sessionId = decoder.decode(payload);
          localStorage.setItem(sessionKey(), sessionId);
          updateSessionLabel();
          refreshSessions();
          break;
        }
        case OP.NAME: {
          sessionName = decoder.decode(payload);
          updateSessionLabel();
          refreshSessions();
          break;
        }
        case OP.EXIT: {
          const code =
            payload.length >= 4
              ? ((payload[0] << 24) |
                  (payload[1] << 16) |
                  (payload[2] << 8) |
                  payload[3]) >>>
                0
              : 0;
          setStatus("process exited (" + code + ")", "bad");
          sessionId = "";
          sessionName = "";
          localStorage.removeItem(sessionKey());
          sessionWrap.hidden = true;
          refreshSessions();
          break;
        }
        case OP.ERROR:
          setStatus("error: " + decoder.decode(payload), "bad");
          break;
        default:
          break;
      }
    };

    socket.onclose = () => {
      if (generation !== wsGeneration) return;
      setStatus("disconnected, retrying…", "bad");
      const delay = Math.min(10000, 500 * Math.pow(2, attempt++));
      reconnectTimer = setTimeout(connect, delay);
    };

    socket.onerror = () => {};
  }

  term.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(frame(OP.INPUT, 0, encoder.encode(data)));
    }
  });

  let resizeTimer = null;
  term.onResize(() => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(frame(OP.RESIZE, 0, resizePayload(term.rows, term.cols)));
      }
    }, 80);
  });

  function refit() {
    try {
      fit.fit();
    } catch (_) {}
  }

  if (window.ResizeObserver) {
    let refitFrame = 0;
    const resizeObserver = new ResizeObserver(() => {
      cancelAnimationFrame(refitFrame);
      refitFrame = requestAnimationFrame(refit);
    });
    resizeObserver.observe(termEl);
  }

  window.addEventListener("resize", refit);
  window.addEventListener("orientationchange", () => setTimeout(refit, 150));
  if (window.visualViewport) {
    window.visualViewport.addEventListener("resize", refit);
  }

  document.querySelectorAll("#keys button").forEach((button) => {
    button.addEventListener("click", () => {
      const seq = button.dataset.seq || "";
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(frame(OP.INPUT, 0, encoder.encode(seq)));
      }
      term.focus();
    });
  });

  document.getElementById("reconnect").addEventListener("click", () => connect());

  let editingName = false;

  function beginRename() {
    if (editingName) return;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    if (!sessionId && !sessionName) return;
    editingName = true;
    sessionNameEl.hidden = true;
    sessionInput.hidden = false;
    sessionSave.hidden = false;
    sessionCancel.hidden = false;
    sessionInput.value = sessionName || sessionId;
    sessionInput.focus();
    sessionInput.select();
  }

  function endRename(save) {
    if (!editingName) return;
    editingName = false;
    const value = sessionInput.value.trim();
    sessionInput.hidden = true;
    sessionSave.hidden = true;
    sessionCancel.hidden = true;
    sessionNameEl.hidden = false;
    sessionInput.value = "";
    if (save && value && value !== sessionName && ws?.readyState === WebSocket.OPEN) {
      ws.send(frame(OP.RENAME, 0, encoder.encode(value)));
    }
  }

  sessionNameEl.addEventListener("dblclick", beginRename);
  sessionSave.addEventListener("click", () => endRename(true));
  sessionCancel.addEventListener("click", () => endRename(false));
  sessionInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      endRename(true);
    } else if (event.key === "Escape") {
      event.preventDefault();
      endRename(false);
    }
  });

  async function loadAgents() {
    try {
      const response = await fetch("/api/agents");
      const agents = await response.json();
      agentEl.innerHTML = "";
      for (const agent of agents) {
        const option = document.createElement("option");
        option.value = agent.name;
        option.textContent = agent.available
          ? agent.name
          : agent.name + " (missing)";
        option.disabled = !agent.available && agent.name !== "shell";
        agentEl.appendChild(option);
      }
      if (!currentAgent) currentAgent = params.get("agent") || "shell";
      if (!agents.some((a) => a.name === currentAgent)) currentAgent = "shell";
      agentEl.value = currentAgent;
      localStorage.setItem("agentty.agent", currentAgent);
    } catch (_) {
      agentEl.innerHTML = '<option value="shell">shell</option>';
      if (!currentAgent) currentAgent = "shell";
    }
  }

  agentEl.addEventListener("change", () => {
    currentAgent = agentEl.value;
    localStorage.setItem("agentty.agent", currentAgent);
    newSession();
  });

  // --- session sidebar ----------------------------------------------------

  const sidebar = document.getElementById("sidebar");
  const sessionList = document.getElementById("session-list");
  const sidebarToggle = document.getElementById("sidebar-toggle");
  let sessionsSignature = "";
  let sessionsTimer = null;

  function setSidebar(visible) {
    sidebar.hidden = !visible;
    localStorage.setItem("agentty.sidebar", visible ? "1" : "0");
    requestAnimationFrame(() => refit());
  }

  const storedSidebar = localStorage.getItem("agentty.sidebar");
  setSidebar(storedSidebar === null ? window.innerWidth > 720 : storedSidebar === "1");

  function selectSession(id, agent) {
    if (!id || id === sessionId) return;
    if (agent) {
      currentAgent = agent;
      if ([...agentEl.options].some((option) => option.value === agent)) {
        agentEl.value = agent;
      }
    }
    sessionId = id;
    sessionName = "";
    localStorage.setItem(sessionKey(), id);
    term.reset();
    connect();
  }

  function newSession() {
    sessionId = generateSessionID();
    sessionName = "";
    localStorage.setItem(sessionKey(), sessionId);
    term.reset();
    connect();
  }

  function renderSessions(sessions) {
    const signature = JSON.stringify([sessionId, sessions]);
    if (signature === sessionsSignature) return;
    sessionsSignature = signature;

    sessionList.innerHTML = "";
    if (sessions.length === 0) {
      const empty = document.createElement("li");
      empty.className = "session-item empty";
      empty.textContent = "No sessions";
      sessionList.appendChild(empty);
      return;
    }

    for (const session of sessions) {
      const item = document.createElement("li");
      item.className = "session-item" + (session.id === sessionId ? " selected" : "");
      item.dataset.id = session.id;
      item.dataset.agent = session.agent || "";

      const dot = document.createElement("span");
      dot.className = "dot" + (session.exited ? " exited" : "");

      const name = document.createElement("span");
      name.className = "name";
      name.textContent = session.name || session.id;

      const meta = document.createElement("span");
      meta.className = "meta";
      meta.textContent =
        session.clients > 0
          ? `${session.agent || "shell"} · ${session.clients}`
          : session.agent || "shell";

      const close = document.createElement("button");
      close.className = "close";
      close.title = "Close session";
      close.textContent = "×";
      close.addEventListener("click", (event) => {
        event.stopPropagation();
        closeSession(session.id);
      });

      item.append(dot, name, meta, close);
      sessionList.appendChild(item);
    }
  }

  async function closeSession(id) {
    if (!id) return;
    if (!window.confirm("Close this session? The agent process will be terminated.")) {
      return;
    }
    try {
      await fetch("/api/sessions/close", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id }),
      });
    } catch (_) {}

    if (id === sessionId) {
      sessionId = generateSessionID();
      sessionName = "";
      localStorage.setItem(sessionKey(), sessionId);
      term.reset();
      connect();
    }
    refreshSessions();
  }

  async function refreshSessions() {
    try {
      const response = await fetch("/api/sessions");
      if (!response.ok) return;
      const sessions = await response.json();
      if (sessionId && !sessions.some((session) => session.id === sessionId)) {
        sessions.unshift({
          id: sessionId,
          name: sessionName || sessionId,
          agent: currentAgent,
          exited: false,
          clients: 1,
        });
      }
      renderSessions(sessions);
    } catch (_) {}
  }

  function startSessionPolling() {
    refreshSessions();
    if (sessionsTimer) return;
    sessionsTimer = setInterval(() => {
      if (!document.hidden) refreshSessions();
    }, 4000);
  }

  sidebarToggle.addEventListener("click", () => setSidebar(sidebar.hidden));
  document.getElementById("sidebar-close").addEventListener("click", () => setSidebar(false));
  document.getElementById("new-session").addEventListener("click", () => {
    newSession();
    if (window.matchMedia("(max-width: 720px)").matches) setSidebar(false);
  });

  sessionList.addEventListener("click", (event) => {
    const item = event.target.closest(".session-item");
    if (!item || item.classList.contains("empty")) return;
    selectSession(item.dataset.id, item.dataset.agent);
    if (window.matchMedia("(max-width: 720px)").matches) setSidebar(false);
  });

  // --- authentication -----------------------------------------------------

  const loginEl = document.getElementById("login");
  const loginForm = document.getElementById("login-form");
  const loginSecret = document.getElementById("login-secret");
  const loginError = document.getElementById("login-error");
  const barEl = document.getElementById("bar");
  const keysEl = document.getElementById("keys");

  let started = false;

  function showLogin(message) {
    loginEl.hidden = false;
    termEl.style.display = "none";
    barEl.style.display = "none";
    keysEl.style.display = "none";
    if (message) loginError.textContent = message;
    loginSecret.focus();
  }

  function hideLogin() {
    loginEl.hidden = true;
    termEl.style.display = "";
    barEl.style.display = "";
    keysEl.style.display = "";
  }

  async function authStatus() {
    try {
      const response = await fetch("/api/auth/status");
      if (!response.ok) return { authenticated: false };
      return await response.json();
    } catch (_) {
      return { authenticated: false };
    }
  }

  async function login(secret) {
    try {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret }),
      });
      return response.ok;
    } catch (_) {
      return false;
    }
  }

  function start() {
    if (started) return;
    started = true;
    startSessionPolling();
    refit();
    loadAgents().then(() => {
      loadSession();
      connect();
    });
  }

  async function boot() {
    const hash = location.hash || "";
    if (hash.startsWith("#pair=")) {
      const secret = decodeURIComponent(hash.slice("#pair=".length));
      history.replaceState(null, "", location.pathname + location.search);
      if (await login(secret)) {
        hideLogin();
        start();
      } else {
        showLogin("That pairing link is invalid or expired.");
      }
      return;
    }

    const status = await authStatus();
    if (status.authenticated) {
      hideLogin();
      start();
    } else {
      showLogin("");
    }
  }

  loginForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    loginError.textContent = "";
    if (await login(loginSecret.value)) {
      hideLogin();
      start();
    } else {
      loginError.textContent = "Invalid token or password.";
    }
  });

  document.getElementById("logout").addEventListener("click", async () => {
    await fetch("/api/auth/logout", { method: "POST" });
    location.reload();
  });

  boot();
})();
