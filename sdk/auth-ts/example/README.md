# @muvee/auth demo

Minimal Vite + React demo of the [`@muvee/auth`](../) SDK. Deployable on muvee
itself: build the included Dockerfile, expose port 8080, you're done.

## Local development

```bash
npm install
npm run dev          # http://localhost:5173
```

For the SDK to talk to a real muvee instance, point the browser at a host that
is a muvee project subdomain (so `/_oauth/providers`, `/_oauth/login-token`
etc. resolve). Easiest path is the deployed demo on muveeai.com; for local
iteration set `MuveeAuth.configure({ baseUrl: 'https://your-project.muveeai.com' })`
and accept that cookies will be third-party.

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
