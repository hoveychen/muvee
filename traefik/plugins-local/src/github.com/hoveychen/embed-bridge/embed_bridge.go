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
// The SDK body is compiled in as a constant (sdkScript) so the plugin has
// no runtime file dependencies — yaegi-friendly, no os.ReadFile.
package embed_bridge

import (
	"bytes"
	"context"
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

	// 2. All other requests: capture downstream response and, if HTML,
	//    splice in the script tag.
	rec := newResponseRecorder(rw)
	e.next.ServeHTTP(rec, req)

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

// responseRecorder buffers the downstream response so the middleware can
// decide post-hoc whether to splice. We don't stream — splicing requires
// finding `</head>` which may straddle chunk boundaries — but HTML pages
// being iframed are typically <100 KB so memorising in full is fine.
type responseRecorder struct {
	wrapped    http.ResponseWriter
	headers    http.Header
	body       *bytes.Buffer
	statusCode int
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
	r.statusCode = statusCode
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
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
` + html2canvasBundle + sdkSelectionScripts

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
