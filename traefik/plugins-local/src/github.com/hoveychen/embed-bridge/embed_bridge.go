// Package embed_bridge is a traefik local plugin that gives every
// muvee-deployed project iframe-bridge support without the project itself
// needing to ship any code. The plugin does two things in the request path:
//
//  1. Intercepts `GET <ScriptPath>` (default `/_embed-bridge.js`) and serves
//     the SDK body inline. This means any URL inside any muvee project can
//     load the SDK with `<script src="/_embed-bridge.js">` regardless of
//     whether that project's webserver knows the path.
//
//  2. For all other requests, runs the downstream handler, captures the
//     response, and — if the response is an HTML document — splices a
//     `<script src="<ScriptPath>" defer></script>` tag immediately before
//     the closing `</head>`. Non-HTML responses (assets, JSON APIs, image
//     bytes, etc.) are streamed through unchanged.
//
// Besides the iframe bridge, the SDK carries the auth-guard (sdkAuthGuardScript),
// which runs on top-level pages too: it sends the user back through login when
// the project's muvee session expires under a long-lived SPA.
//
// The SDK body is compiled in as a constant (sdkScript) so the plugin has
// no runtime file dependencies — yaegi-friendly, no os.ReadFile.
package embed_bridge

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Config is the user-facing config block read from traefik dynamic config.
type Config struct {
	// ScriptPath is the URL path the injected <script> tag points at and
	// the path the plugin intercepts to serve the SDK body. Defaults to
	// `/_embed-bridge.js` when empty.
	ScriptPath string `json:"scriptPath,omitempty" yaml:"scriptPath,omitempty"`
}

// CreateConfig is required by traefik plugin contract.
func CreateConfig() *Config {
	return &Config{ScriptPath: "/_embed-bridge.js"}
}

// EmbedBridge is the middleware handler.
type EmbedBridge struct {
	next       http.Handler
	name       string
	scriptPath string
}

// New is required by traefik plugin contract.
func New(_ context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	scriptPath := config.ScriptPath
	if scriptPath == "" {
		scriptPath = "/_embed-bridge.js"
	}
	return &EmbedBridge{next: next, name: name, scriptPath: scriptPath}, nil
}

func (e *EmbedBridge) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// HTTP Upgrade requests (WebSocket, h2c, …) terminate in a 101 Switching
	// Protocols and require Traefik's reverse proxy to hijack the client conn.
	// Our responseRecorder is a buffering wrapper without http.Hijacker, which
	// breaks the upgrade with "can't switch protocols using non-Hijacker
	// ResponseWriter type". Pass through with the original rw so Hijacker
	// survives — there is no HTML body to splice on an upgrade anyway.
	if req.Header.Get("Upgrade") != "" {
		e.next.ServeHTTP(rw, req)
		return
	}

	// 1. /_embed-bridge.js short-circuit — serve the SDK body, do not
	//    forward to the underlying project.
	if req.Method == http.MethodGet && req.URL.Path == e.scriptPath {
		rw.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		// Cache modestly. The SDK rarely changes; if it does, traefik gets
		// updated and the new content immediately ships under the same path.
		rw.Header().Set("Cache-Control", "public, max-age=300")
		rw.Header().Set("Content-Length", strconv.Itoa(len(sdkScript)))
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(sdkScript))
		return
	}

	// A compressed HTML body cannot be spliced, so ask the upstream for an
	// identity-encoded document. Only page loads are touched — assets and API
	// calls keep their compression.
	if isDocumentRequest(req) {
		req.Header.Del("Accept-Encoding")
	}

	// 2. All other requests: capture downstream response and, if HTML,
	//    splice in the script tag. The recorder only buffers once it has seen
	//    a text/html Content-Type; anything else streams straight through (see
	//    responseRecorder.WriteHeader).
	rec := newResponseRecorder(rw)
	e.next.ServeHTTP(rec, req)

	// The response was streamed to the client as it arrived — nothing left
	// to splice or flush.
	if rec.passthrough {
		return
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		rec.flush(rw)
		return
	}

	// 3xx redirects, 304 Not Modified etc. typically have empty bodies.
	// Don't try to splice — just pass through.
	if rec.statusCode >= 300 && rec.statusCode < 400 {
		rec.flush(rw)
		return
	}
	if rec.body.Len() == 0 {
		rec.flush(rw)
		return
	}

	body := rec.body.Bytes()
	// Upstreams that compress regardless of Accept-Encoding: gzip is the only
	// encoding we can undo here (no brotli/zstd in the stdlib); anything else
	// passes through unspliced.
	switch strings.ToLower(rec.Header().Get("Content-Encoding")) {
	case "", "identity":
	case "gzip":
		decoded, err := gunzip(body)
		if err != nil {
			rec.flush(rw)
			return
		}
		body = decoded
		rec.Header().Del("Content-Encoding")
	default:
		rec.flush(rw)
		return
	}
	idx := bytes.Index(body, []byte("</head>"))
	if idx < 0 {
		// No </head> — not a complete HTML document we can splice safely.
		// Pass through unchanged.
		rec.flush(rw)
		return
	}

	snippet := []byte(`<script src="` + e.scriptPath + `" defer></script>`)
	out := make([]byte, 0, len(body)+len(snippet))
	out = append(out, body[:idx]...)
	out = append(out, snippet...)
	out = append(out, body[idx:]...)

	// Copy through all downstream headers, then fix Content-Length to match
	// the new body. Do NOT delete other headers (Set-Cookie, ETag, etc).
	for k, vs := range rec.Header() {
		for _, v := range vs {
			rw.Header().Add(k, v)
		}
	}
	rw.Header().Set("Content-Length", strconv.Itoa(len(out)))
	// Drop any pre-existing strong validators — the body has changed.
	rw.Header().Del("ETag")
	rw.Header().Del("Last-Modified")

	status := rec.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	rw.WriteHeader(status)
	rw.Write(out)
}

// isDocumentRequest reports whether req is a browser page load (top-level or
// iframe) — the only kind of request whose response we may splice into.
func isDocumentRequest(req *http.Request) bool {
	switch req.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "frame", "nested-document":
		return true
	case "":
		return strings.Contains(req.Header.Get("Accept"), "text/html")
	default:
		return false
	}
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// responseRecorder buffers the downstream response so the middleware can
// decide post-hoc whether to splice. Splicing requires finding `</head>`,
// which may straddle chunk boundaries, so an HTML body has to be held in full
// — iframed pages are typically <100 KB, which is fine to memorise.
//
// Everything else must NOT be held. The Content-Type is known as soon as the
// handler calls WriteHeader, so at that point a non-HTML response switches to
// passthrough: headers go out immediately and every subsequent Write lands on
// the real ResponseWriter. Without this, a long-lived stream (SSE, chunked
// NDJSON) is buffered until the handler returns — which for an endless stream
// never happens, so the client receives nothing at all. It also kept every
// large download in memory for the duration of the response.
type responseRecorder struct {
	wrapped    http.ResponseWriter
	headers    http.Header
	body       *bytes.Buffer
	statusCode int
	// passthrough is set once we know the response is not text/html; from
	// then on the recorder is a thin forwarder to wrapped.
	passthrough bool
	// wroteHeader guards the one-shot decision in WriteHeader.
	wroteHeader bool
}

func newResponseRecorder(wrapped http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		wrapped: wrapped,
		headers: make(http.Header),
		body:    &bytes.Buffer{},
	}
}

func (r *responseRecorder) Header() http.Header { return r.headers }

func (r *responseRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.statusCode = statusCode

	// Only a text/html body can be spliced, so only that one needs buffering.
	// Note an absent Content-Type also lands here and streams through — which
	// matches the pre-existing behaviour, since the splice below likewise only
	// fired on an explicit text/html.
	if strings.Contains(strings.ToLower(r.headers.Get("Content-Type")), "text/html") {
		return
	}

	r.passthrough = true
	for k, vs := range r.headers {
		for _, v := range vs {
			r.wrapped.Header().Add(k, v)
		}
	}
	r.wrapped.WriteHeader(statusCode)
	// Commit the header line immediately — an SSE client waits on the response
	// headers before it will report the stream as open.
	r.flushWrapped()
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	// net/http semantics: the first Write implies WriteHeader(200). Run the
	// same decision so a handler that never calls WriteHeader still streams.
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.passthrough {
		n, err := r.wrapped.Write(b)
		// Push the chunk out now instead of waiting to be asked. Traefik's
		// reverse proxy drives streaming by type-asserting http.Flusher on the
		// ResponseWriter it was handed — but this plugin is interpreted by
		// yaegi, whose shim around the recorder exposes only Header/Write/
		// WriteHeader. Our Flush method is invisible from there, so nothing
		// downstream will ever flush and the chunk would sit in net/http's
		// 4 KB buffer until the handler returns. For SSE that is never.
		r.flushWrapped()
		return n, err
	}
	return r.body.Write(b)
}

// flushWrapped flushes the real ResponseWriter when it supports it.
func (r *responseRecorder) flushWrapped() {
	if f, ok := r.wrapped.(http.Flusher); ok {
		f.Flush()
	}
}

// Flush forwards to the real ResponseWriter once streaming, so an SSE handler
// can push each event as it is produced. While buffering an HTML body there is
// nothing to flush yet.
func (r *responseRecorder) Flush() {
	if !r.passthrough {
		return
	}
	r.flushWrapped()
}

// Hijack forwards to the real ResponseWriter. A handler may take over the
// connection before writing anything (h2c, a nested proxy), at which point the
// recorder is still in the chain and must not swallow the capability.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.wrapped.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// flush copies the captured response unchanged to the real ResponseWriter.
// Used for non-HTML / no-</head> / redirect cases.
func (r *responseRecorder) flush(rw http.ResponseWriter) {
	for k, vs := range r.headers {
		for _, v := range vs {
			rw.Header().Add(k, v)
		}
	}
	status := r.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	rw.WriteHeader(status)
	rw.Write(r.body.Bytes())
}

// sdkScript is the body of /_embed-bridge.js.
//
// Originally a mirror of muvee/web/public/_embed-bridge.js, but diverged
// starting with the v1 `embed:selection` + v2 `embed:selection-screenshot`
// listeners (proposal: agent-workspace/docs/proposals/
// muvee-embed-bridge-selection-screenshot.md). The traefik plugin is now
// the authoritative SDK surface for muvee-deployed apps — the web/public
// file retains the meta-only listener for dev/standalone access.
//
// No runtime fs read because yaegi-loaded plugins shouldn't pull external
// files at startup (the path would be sandbox-dependent).
//
// Layout, in order, of the body served at /_embed-bridge.js:
//   1. Meta upstream (embed:meta) — title/url, this const body.
//   2. html2canvasBundle (separate file html2canvas.go) — DOM rasterizer
//      needed by v2; defines the global `html2canvas` function.
//   3. sdkSelectionScripts (below) — v1 `embed:selection` text fallback
//      plus v2 `embed:selection-screenshot` viewport raster.
//   4. sdkAnnotateScript — shell-driven element annotation.
//   5. sdkAuthGuardScript — expired-session relogin, top-level pages too.
const sdkScript = `// _embed-bridge.js — bridge SDK injected by muvee traefik plugin so any
// muvee-deployed app surfaces its real <title> + URL to a host page that
// iframes it (e.g. agent-workspace ` + "`/embed`" + ` page).
//
// Posts {type:"embed:meta", title, url} to window.parent whenever
// document.title or location.href changes. No-op when not iframed.
(function () {
  if (window.parent === window) return;

  var lastSent = { title: "", url: "" };

  function send() {
    var payload = {
      type: "embed:meta",
      title: document.title || "",
      url: window.location.href,
    };
    if (payload.title === lastSent.title && payload.url === lastSent.url) return;
    lastSent = { title: payload.title, url: payload.url };
    try {
      window.parent.postMessage(payload, "*");
    } catch (_) {}
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", send, { once: true });
  } else {
    send();
  }
  window.addEventListener("load", send);

  function attachTitleObserver() {
    var head = document.head || document.getElementsByTagName("head")[0];
    if (!head) return;
    var observer = new MutationObserver(send);
    observer.observe(head, { subtree: true, childList: true, characterData: true });
  }
  if (document.head) attachTitleObserver();
  else document.addEventListener("DOMContentLoaded", attachTitleObserver, { once: true });

  function patch(method) {
    var orig = history[method];
    if (typeof orig !== "function") return;
    history[method] = function () {
      var ret = orig.apply(this, arguments);
      setTimeout(send, 0);
      return ret;
    };
  }
  patch("pushState");
  patch("replaceState");
  window.addEventListener("popstate", function () { setTimeout(send, 0); });
  window.addEventListener("hashchange", function () { setTimeout(send, 0); });
})();
` + html2canvasBundle + sdkSelectionScripts + sdkAnnotateScript + sdkAuthGuardScript

// sdkSelectionScripts is the v1 + v2 selection-upstream blocks concatenated
// onto sdkScript after html2canvasBundle. v1 sends `embed:selection` (plain
// text + the surrounding paragraph) and is the always-on fallback. v2 sends
// `embed:selection-screenshot` (PNG/JPEG data URL + viewport-relative bbox)
// and runs in parallel; shell-side dedupes by event-arrival order so the
// user only sees one toast per selection.
//
// Both listeners ship verbatim from agent-workspace/docs/embed-bridge-
// protocol.md § SDK reference — do NOT diverge from that spec without
// updating the protocol doc first.
const sdkSelectionScripts = `
// embed:selection — v1 text-only upstream (always-on fallback for pages
// where html2canvas can't rasterize, e.g. tainted canvases).
(function () {
  // Only run when actually embedded; on direct (top-level) access there is
  // no parent shell to receive the message, so the capture is wasted work.
  if (window.parent === window) return;

  let timer = null;
  let lastText = '';

  document.addEventListener('selectionchange', () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      const sel = window.getSelection();
      if (!sel) return;
      let text = sel.toString().trim();
      if (!text || text === lastText) return;
      if (text.length > 4096) text = text.slice(0, 4096) + '…';
      lastText = text;

      // surrounding: textContent of the block-level element the selection
      // anchors inside; gives the agent paragraph-level context.
      let surrounding;
      try {
        let node = sel.anchorNode;
        while (node && node !== document.body) {
          if (
            node.nodeType === 1 &&
            /^(P|DIV|ARTICLE|SECTION|LI|TD|BLOCKQUOTE)$/.test(node.tagName)
          ) {
            surrounding = node.textContent.trim();
            if (surrounding.length > 2048) {
              surrounding = surrounding.slice(0, 2048) + '…';
            }
            break;
          }
          node = node.parentNode;
        }
      } catch (e) {
        /* ignore */
      }

      parent.postMessage(
        {
          type: 'embed:selection',
          text,
          url: location.href,
          title: document.title,
          surrounding,
        },
        '*',
      );
    }, 300);
  });
})();

// embed:selection-screenshot — v2 viewport raster upstream. Runs only when
// html2canvas is present and the canvas isn't tainted by cross-origin
// assets; otherwise drops the event silently and lets v1 cover it.
(function () {
  // Only run when actually embedded; on direct (top-level) access the red
  // capture overlay would flash on every text selection for no reason —
  // there is no parent shell to receive the screenshot.
  if (window.parent === window) return;
  if (typeof html2canvas !== 'function') return;

  let timer = null;
  let lastFingerprint = '';

  async function captureSelection() {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return null;
    const text = sel.toString().trim();
    const range = sel.getRangeAt(0);
    const rect = range.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return null;

    // Document-relative red overlay so html2canvas captures it as part of
    // the body raster; removed immediately after capture.
    const overlay = document.createElement('div');
    overlay.style.cssText = [
      'position:absolute',
      'left:' + (rect.left + window.scrollX) + 'px',
      'top:' + (rect.top + window.scrollY) + 'px',
      'width:' + rect.width + 'px',
      'height:' + rect.height + 'px',
      'outline:2px solid red',
      'background:rgba(255,0,0,.15)',
      'pointer-events:none',
      'z-index:2147483647',
    ].join(';');
    document.body.appendChild(overlay);

    let canvas;
    try {
      canvas = await html2canvas(document.body, {
        x: window.scrollX,
        y: window.scrollY,
        width: window.innerWidth,
        height: window.innerHeight,
        scale: window.devicePixelRatio || 1,
        useCORS: true,
        logging: false,
        backgroundColor: null,
      });
    } catch (e) {
      overlay.remove();
      return null;
    }
    overlay.remove();

    let dataUrl;
    try {
      dataUrl = canvas.toDataURL('image/png');
      if (dataUrl.length > 4 * 1024 * 1024 * 1.37) {
        dataUrl = canvas.toDataURL('image/jpeg', 0.85);
      }
      if (dataUrl.length > 4 * 1024 * 1024 * 1.37) {
        return null;
      }
    } catch (e) {
      return null;
    }

    return {
      dataUrl,
      bbox: {
        x: Math.round(rect.left),
        y: Math.round(rect.top),
        w: Math.round(rect.width),
        h: Math.round(rect.height),
      },
      text: text.length > 4096 ? text.slice(0, 4096) + '…' : text,
    };
  }

  document.addEventListener('selectionchange', () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(async () => {
      const sel = window.getSelection();
      if (!sel || !sel.toString().trim()) return;
      // Fingerprint by text + range count so a drag across the same word
      // doesn't fire a dozen captures.
      const fp =
        sel.toString().trim().slice(0, 200) + '|' + sel.rangeCount;
      if (fp === lastFingerprint) return;
      lastFingerprint = fp;

      const captured = await captureSelection();
      if (!captured) return;

      parent.postMessage(
        {
          type: 'embed:selection-screenshot',
          screenshot: captured.dataUrl,
          bbox: captured.bbox,
          url: location.href,
          title: document.title,
          text: captured.text,
        },
        '*',
      );
    }, 300);
  });
})();
`

// sdkAnnotateScript is the annotate-mode block, concatenated onto sdkScript
// after the selection blocks. It is the only SDK code that listens for
// messages FROM window.parent (embed:annotate-start / embed:annotate-stop) —
// the shell toolbar toggle drives it. While active it highlights the element
// under the cursor and, on click, rasterizes just that element (clipped
// html2canvas region) and posts `embed:annotate-element` (text + screenshot +
// bbox). The shell appends each one straight into the focused task composer —
// no per-click confirmation, so the user can click many elements in a row.
// See agent-workspace/docs/embed-bridge-protocol.md for the contract.
const sdkAnnotateScript = `
// embed:annotate-* — shell-driven element annotation. Mutually exclusive with
// the selection blocks above in practice (the user clicks instead of
// selecting); the shell ignores selection events while a toggle is on anyway.
(function () {
  var on = false;
  var hovered = null;
  var prevOutline = '';
  var prevCursor = '';
  var busy = false;

  function clearHL() {
    if (hovered) {
      try {
        hovered.style.outline = prevOutline;
        hovered.style.cursor = prevCursor;
      } catch (e) {}
      hovered = null;
    }
  }

  function targetOf(e) {
    if (e.composedPath) {
      var p = e.composedPath();
      if (p && p.length) return p[0];
    }
    return e.target;
  }

  function onMove(e) {
    if (!on) return;
    var el = targetOf(e);
    if (!el || el.nodeType !== 1) return;
    if (el === document.body || el === document.documentElement) return;
    if (el === hovered) return;
    clearHL();
    hovered = el;
    try {
      prevOutline = el.style.outline;
      prevCursor = el.style.cursor;
      el.style.outline = '2px solid #6366f1';
      el.style.cursor = 'crosshair';
    } catch (e2) {}
  }

  async function captureElement(el) {
    if (typeof html2canvas !== 'function') return null;
    var rect = el.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return null;
    // Drop the hover outline so it isn't baked into the raster.
    var savedOutline = el.style.outline;
    try { el.style.outline = ''; } catch (e) {}
    var canvas;
    try {
      canvas = await html2canvas(document.body, {
        x: rect.left + window.scrollX,
        y: rect.top + window.scrollY,
        width: rect.width,
        height: rect.height,
        scale: window.devicePixelRatio || 1,
        useCORS: true,
        logging: false,
        backgroundColor: null,
      });
    } catch (e) {
      try { el.style.outline = savedOutline; } catch (e2) {}
      return null;
    }
    try { el.style.outline = savedOutline; } catch (e) {}
    var dataUrl;
    try {
      dataUrl = canvas.toDataURL('image/png');
      if (dataUrl.length > 4 * 1024 * 1024 * 1.37) {
        dataUrl = canvas.toDataURL('image/jpeg', 0.85);
      }
      if (dataUrl.length > 4 * 1024 * 1024 * 1.37) return null;
    } catch (e) {
      return null;
    }
    return {
      dataUrl: dataUrl,
      bbox: {
        x: Math.round(rect.left),
        y: Math.round(rect.top),
        w: Math.round(rect.width),
        h: Math.round(rect.height),
      },
    };
  }

  async function onClick(e) {
    if (!on) return;
    e.preventDefault();
    e.stopPropagation();
    if (busy) return;
    busy = true;
    try {
      var el = targetOf(e);
      if (!el || el.nodeType !== 1) return;
      var text = '';
      try { text = (el.textContent || '').trim(); } catch (e2) {}
      if (text.length > 4096) text = text.slice(0, 4096) + '…';
      var shot = await captureElement(el);
      parent.postMessage(
        {
          type: 'embed:annotate-element',
          text: text,
          url: location.href,
          title: document.title,
          screenshot: shot ? shot.dataUrl : '',
          bbox: shot ? shot.bbox : { x: 0, y: 0, w: 0, h: 0 },
        },
        '*'
      );
    } finally {
      busy = false;
    }
  }

  function start() {
    if (on) return;
    on = true;
    document.addEventListener('mousemove', onMove, true);
    document.addEventListener('click', onClick, true);
  }
  function stop() {
    on = false;
    clearHL();
    document.removeEventListener('mousemove', onMove, true);
    document.removeEventListener('click', onClick, true);
  }

  window.addEventListener('message', function (e) {
    if (e.source !== window.parent) return;
    var t = e.data && e.data.type;
    if (t === 'embed:annotate-start') start();
    else if (t === 'embed:annotate-stop') stop();
  });
})();
`

// sdkAuthGuardScript sends the user back through login when the project's
// muvee session expires. muvee-authservice answers an expired session's
// fetch / XHR with 401 + `X-Muvee-Auth: login-required` (a page load still
// gets the 302 to the login page), so an SPA that stays open past its session
// would otherwise just keep failing its API calls. On that signal the guard
// reloads the page: the reload is a navigation, ForwardAuth records it as the
// post-login return URL and redirects to sign-in, and the user lands back on
// the same page. Coming back to a hidden tab also probes the session, so a
// stale tab re-logs-in before the user starts clicking.
//
// Unlike the blocks above it runs on top-level pages as well as iframes. It is
// inert on projects without muvee login: their responses never carry the
// header.
const sdkAuthGuardScript = `
// muvee auth-guard — relogin when the muvee session expires.
(function () {
  if (window.__muveeAuthGuard) return;
  window.__muveeAuthGuard = true;

  var origFetch = window.fetch;
  var RELOAD_KEY = '__muvee_auth_reload_at';

  function expired(status, header) {
    return status === 401 && header === 'login-required';
  }

  function relogin() {
    // Never reload in a loop: if a reload did not fix it, leave the page be.
    try {
      var last = Number(sessionStorage.getItem(RELOAD_KEY)) || 0;
      if (Date.now() - last < 30000) return;
      sessionStorage.setItem(RELOAD_KEY, String(Date.now()));
    } catch (e) {}
    location.reload();
  }

  if (typeof origFetch === 'function') {
    window.fetch = function () {
      return origFetch.apply(window, arguments).then(function (res) {
        try {
          if (expired(res.status, res.headers.get('X-Muvee-Auth'))) relogin();
        } catch (e) {}
        return res;
      });
    };
  }

  if (window.XMLHttpRequest) {
    var origSend = XMLHttpRequest.prototype.send;
    XMLHttpRequest.prototype.send = function () {
      var xhr = this;
      xhr.addEventListener('loadend', function () {
        try {
          if (expired(xhr.status, xhr.getResponseHeader('X-Muvee-Auth'))) relogin();
        } catch (e) {}
      });
      return origSend.apply(xhr, arguments);
    };
  }

  var lastProbe = 0;
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState !== 'visible' || typeof origFetch !== 'function') return;
    var now = Date.now();
    if (now - lastProbe < 60000) return;
    lastProbe = now;
    origFetch(location.href, { method: 'HEAD', credentials: 'same-origin', cache: 'no-store' })
      .then(function (res) {
        if (expired(res.status, res.headers.get('X-Muvee-Auth'))) relogin();
      })
      .catch(function () {});
  });
})();
`
