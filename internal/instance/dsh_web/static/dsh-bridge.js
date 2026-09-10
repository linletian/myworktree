(() => {
  "use strict";
  const Native = window.WebSocket;

  class MWWebSocket {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSING = 2;
    static CLOSED = 3;

    constructor(url, protocols) {
      const target = String(url);
      if (!target.includes("/api/remote.mux")) return new Native(url, protocols);

      this.url = target;
      this.protocol = "";
      this.extensions = "";
      this.binaryType = "blob";
      this.bufferedAmount = 0;
      this.readyState = MWWebSocket.CONNECTING;
      this.listeners = new Map();
      this.handlers = new Map();
      this.queue = Promise.resolve();
      this.opened = new Promise((resolve) => { this.resolveOpen = resolve; });

      const native = new Native(url, protocols);
      this.native = native;
      let settled = false;
      const timer = setTimeout(() => fallback(), 1000);
      const fallback = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        native.close();
        this.startBridge(target);
      };
      native.addEventListener("open", (event) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        this.readyState = MWWebSocket.OPEN;
        this.protocol = native.protocol;
        this.extensions = native.extensions;
        this.resolveOpen();
        this.dispatch("open", event);
      });
      native.addEventListener("message", (event) => { if (settled && this.native) this.dispatch("message", event); });
      native.addEventListener("error", (event) => {
        if (!settled) fallback();
        else if (this.native) this.dispatch("error", event);
      });
      native.addEventListener("close", (event) => {
        if (!settled) fallback();
        else if (this.native) {
          this.readyState = MWWebSocket.CLOSED;
          this.dispatch("close", event);
        }
      });
    }

    startBridge(target) {
      this.native = null;
      const bytes = new Uint8Array(8);
      crypto.getRandomValues(bytes);
      this.connId = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
      const sourceURL = new URL(target, window.location.href);
      sourceURL.protocol = sourceURL.protocol === "wss:" ? "https:" : "http:";
      sourceURL.search = `?mwbridge=1&conn=${this.connId}`;
      const es = new EventSource(sourceURL);
      this.eventSource = es;
      es.addEventListener("open", () => {
        this.readyState = MWWebSocket.OPEN;
        this.resolveOpen();
        this.dispatch("open", new Event("open"));
      }, { once: true });
      es.onmessage = (event) => this.dispatch("message", new MessageEvent("message", { data: event.data }));
      es.addEventListener("bin", (event) => {
        const raw = atob(event.data);
        const data = Uint8Array.from(raw, (char) => char.charCodeAt(0));
        this.dispatch("message", new MessageEvent("message", { data }));
      });
      es.addEventListener("close", () => this.finishClose(1006, "bridge"));
      es.addEventListener("bridgeerror", (event) => {
        let reason = "bridge";
        try { reason = JSON.parse(event.data).reason || reason; } catch (_) {}
        this.finishClose(1006, reason);
      });
      es.addEventListener("error", () => {
        if (es.readyState === EventSource.CLOSED) this.finishClose(1006, "bridge");
      });
    }

    send(data) {
      if (this.native) {
        this.native.send(data);
        return;
      }
      if (this.readyState === MWWebSocket.CLOSED) return;
      this.queue = this.queue.then(() => this.opened).then(async () => {
        let body = data;
        let binary = false;
        if (typeof data !== "string") {
          const bytes = data instanceof Blob ? new Uint8Array(await data.arrayBuffer()) :
            data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
          let raw = "";
          for (const byte of bytes) raw += String.fromCharCode(byte);
          body = btoa(raw);
          binary = true;
        }
        await fetch(`/api/remote.mux?mwbridge=1&conn=${this.connId}${binary ? "&bin=1" : ""}`, { method: "POST", body });
      });
    }

    close(code = 1000, reason = "") {
      if (this.native) {
        this.readyState = MWWebSocket.CLOSING;
        this.native.close(code, reason);
        return;
      }
      if (this.readyState === MWWebSocket.CLOSED) return;
      this.readyState = MWWebSocket.CLOSING;
      this.queue = this.queue.then(() => fetch(`/api/remote.mux?mwbridge=1&conn=${this.connId}&close=1`, { method: "POST" }))
        .finally(() => {
          if (this.eventSource) this.eventSource.close();
          this.finishClose(code, reason);
        });
    }

    finishClose(code, reason) {
      if (this.readyState === MWWebSocket.CLOSED) return;
      this.readyState = MWWebSocket.CLOSED;
      // Settle the open gate: sends already queued on this.opened drain
      // and fail closed (their POST hits a dead bridge) instead of
      // hanging for the shim's lifetime.
      if (this.resolveOpen) this.resolveOpen();
      if (this.eventSource) this.eventSource.close();
      this.dispatch("close", new CloseEvent("close", { code, reason }));
    }

    addEventListener(type, listener, options) {
      const entries = this.listeners.get(type) || [];
      entries.push({ listener, once: Boolean(options && typeof options === "object" && options.once) });
      this.listeners.set(type, entries);
    }

    removeEventListener(type, listener) {
      const entries = this.listeners.get(type) || [];
      this.listeners.set(type, entries.filter((entry) => entry.listener !== listener));
    }

    dispatch(type, event) {
      const handler = this.handlers.get(type);
      if (handler) handler.call(this, event);
      const entries = this.listeners.get(type) || [];
      for (const entry of [...entries]) {
        if (typeof entry.listener === "function") entry.listener.call(this, event);
        else if (entry.listener && entry.listener.handleEvent) entry.listener.handleEvent(event);
        if (entry.once) this.removeEventListener(type, entry.listener);
      }
    }

    set onopen(value) { this.handlers.set("open", value); }
    get onopen() { return this.handlers.get("open") || null; }
    set onmessage(value) { this.handlers.set("message", value); }
    get onmessage() { return this.handlers.get("message") || null; }
    set onerror(value) { this.handlers.set("error", value); }
    get onerror() { return this.handlers.get("error") || null; }
    set onclose(value) { this.handlers.set("close", value); }
    get onclose() { return this.handlers.get("close") || null; }
  }

  window.WebSocket = MWWebSocket;
})();
