# @muvee/auth demo

Minimal Vite + React demo of the [`@muvee/auth`](../) SDK. Deployable on muvee
itself: build the included Dockerfile, expose port 8080, you're done.

## Local development

```bash
npm install
npm run dev          # http://localhost:5173
```

For the SDK to talk to a real muvee instance, point the browser at a host that
is a muvee project subdomain (so `/_oauth/providers`, `/_oauth/login`,
`/_oauth/userinfo` etc. resolve). Sign-in opens a popup on that host, so call
`signIn` from a click handler (the demo does). `baseUrl` must be the same
origin as the page: in 0.2.0 you cannot point a `localhost` page at a remote
project (the popup notification, `BroadcastChannel` and cookie-based
`getUser()` are same-origin only). Debug by deploying the demo under a project
subdomain on your muvee instance.

## Deploy to muvee

```bash
muveectl projects create \
  --name auth-sdk-demo \
  --git-url https://github.com/hoveychen/muvee.git \
  --branch main \
  --dockerfile sdk/auth-ts/example/Dockerfile \
  --domain auth-sdk-demo
muveectl projects deploy <project_id>
```

The container listens on port 8080, which is what Muvee expects.
