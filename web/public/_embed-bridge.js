// _embed-bridge.js — bridge SDK for pages that may be iframed inside the
// agent-workspace `/embed` host page.
//
// When the host page iframes a muvee-deployed app, browser same-origin
// policy prevents the host from reading `iframe.contentDocument.title`
// (different subdomain = different origin). To still surface the real,
// up-to-date title in the host's breadcrumb, embedded apps include this
// script which posts {type:"embed:meta", title, url} to window.parent
// whenever the document title or URL changes.
//
// Drop-in usage from any HTML head:
//
//     <script src="/_embed-bridge.js" defer></script>
//
// Safety: the script is a no-op when the page is not iframed (so it stays
// invisible in standalone use), it only posts to window.parent (never any
// other window), and it sends with targetOrigin "*" (the host page is
// expected to verify event.origin against its iframe.src origin, which is
// already what agent-workspace's EmbedPage does).
(function () {
  if (window.parent === window) return; // not iframed — nothing to bridge

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
      // targetOrigin "*" is fine here: the data is the page's own
      // already-public title + URL, and the host verifies origin on receipt.
      window.parent.postMessage(payload, "*");
    } catch (_) {
      /* parent window may have navigated away; ignore */
    }
  }

  // 1. Initial post once the document is ready (covers SPAs that set the
  //    title synchronously during boot and never touch it again).
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", send, { once: true });
  } else {
    send();
  }
  // Cover edge cases where title resolves after window.load (e.g. async
  // bundles that swap it once data arrives).
  window.addEventListener("load", send);

  // 2. <title> mutation observer — SPAs typically set title on every route
  //    change. Watching the head's <title> child node catches that without
  //    relying on framework hooks.
  function attachTitleObserver() {
    var head = document.head || document.getElementsByTagName("head")[0];
    if (!head) return;
    var observer = new MutationObserver(send);
    observer.observe(head, { subtree: true, childList: true, characterData: true });
  }
  if (document.head) attachTitleObserver();
  else document.addEventListener("DOMContentLoaded", attachTitleObserver, { once: true });

  // 3. URL change tracking — pushState/replaceState are silent navigations
  //    that don't fire popstate, so monkey-patch them to emit our event.
  function patch(method) {
    var orig = history[method];
    if (typeof orig !== "function") return;
    history[method] = function () {
      var ret = orig.apply(this, arguments);
      // Defer one tick so React/etc. have a chance to update document.title
      // before we read it.
      setTimeout(send, 0);
      return ret;
    };
  }
  patch("pushState");
  patch("replaceState");
  window.addEventListener("popstate", function () {
    setTimeout(send, 0);
  });
  window.addEventListener("hashchange", function () {
    setTimeout(send, 0);
  });
})();
