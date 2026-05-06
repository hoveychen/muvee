---
title: Embed Bridge
sidebar_position: 50
---

# Embed Bridge

When a host page (e.g. **agent-workspace**'s `/embed` view) loads a
muvee-deployed project inside an `<iframe>`, browser same-origin policy
prevents the host from reading `iframe.contentDocument.title`. To still
surface the embedded page's real title and current URL in the host's
breadcrumb, every muvee project automatically gets a tiny SDK injected
into its HTML responses by Traefik. The SDK posts `{title, url}` updates
to `window.parent` whenever the title or URL changes.

**You do not need to do anything in your project.** This page is purely
informational.

## How it works

Two pieces:

1. **`embed-bridge` Traefik plugin** — a local plugin
   ([source](https://github.com/hoveychen/muvee/tree/main/traefik/plugins-local/src/github.com/hoveychen/embed-bridge))
   loaded by Traefik via `experimental.localPlugins`. The plugin
   intercepts every HTTP response routed through a project's Traefik
   router; if the response is `text/html`, it splices a
   `<script src="/_embed-bridge.js" defer></script>` tag immediately
   before `</head>`. Non-HTML responses (assets, JSON APIs, image bytes,
   …) are streamed through unchanged.
2. **Auto-attach in `muvee-server`** — `handleTraefikConfig` (in
   `internal/api/server.go`) appends `embed-bridge@file` to the middleware
   chain of every user-facing project router (deployments, live tunnels,
   and domain-only placeholders). System routers (OAuth bypass, device-flow,
   authservice) are intentionally excluded.

The SDK itself is served by the plugin from `/_embed-bridge.js`, so the
injected `<script src=…>` resolves regardless of whether your project's
underlying webserver knows that path.

## What gets posted

When `document.title` or `window.location.href` changes, the SDK posts
this message to `window.parent`:

```json
{ "type": "embed:meta", "title": "Current document.title", "url": "https://your-app.muvee.example.com/current/path" }
```

It posts on:

- initial `DOMContentLoaded` and `window.load`
- `<title>` mutations (covers SPAs that swap title on every route change)
- `history.pushState`, `history.replaceState`, `popstate`, `hashchange`

`targetOrigin` is `*` because the data is the page's own already-public
title and URL. Hosts must verify `event.origin` against the iframe's
`src` origin (this is what agent-workspace's `EmbedPage` does).

## What it does NOT do

- It does not send any payload other than title + URL. There is no auth
  token, cookie, or DOM content.
- It does not call any API. Pure browser-side, ~30 lines of vanilla JS.
- It is a no-op when the page is not iframed (`window.parent === window`),
  so it costs nothing for direct visitors.

## Verifying

After your project deploys, you can verify the injection from a shell:

```sh
# 1. The SDK is served at /_embed-bridge.js on every project subdomain.
curl -sI https://your-app.muvee.example.com/_embed-bridge.js | head -3
# → HTTP/2 200
# → content-type: application/javascript; charset=utf-8

# 2. Your project's HTML responses now contain the script tag.
curl -s https://your-app.muvee.example.com/ | grep -o '_embed-bridge.js'
# → _embed-bridge.js
```

Then, from the agent-workspace side, a tenant admin adds your project's
URL to the **Embedded Links** tab. Clicking the new sidebar entry opens
the URL in `/embed`; the breadcrumb shows your project's actual `<title>`
and updates live as you navigate inside the iframe.

## Opting out

The plugin is enabled for every project router by default. If a
specific project must not have its HTML mutated (e.g. it ships strict
[Subresource Integrity](https://developer.mozilla.org/docs/Web/Security/Subresource_Integrity)
hashes for its `index.html` shell), file an issue against muvee — opt-out
via DB column or docker label is straightforward to add but not yet
implemented.

## Source

- Plugin: [traefik/plugins-local/src/github.com/hoveychen/embed-bridge/](https://github.com/hoveychen/muvee/tree/main/traefik/plugins-local/src/github.com/hoveychen/embed-bridge)
- Standalone SDK file (mirror, kept in sync): `web/public/_embed-bridge.js`
- Auto-attach: [`attachEmbedBridge`](https://github.com/hoveychen/muvee/blob/main/internal/api/server.go) in `internal/api/server.go`
- Traefik config: `traefik/traefik.yml` (`experimental.localPlugins`) + `traefik/dynamic.yml` (`embed-bridge` middleware)
