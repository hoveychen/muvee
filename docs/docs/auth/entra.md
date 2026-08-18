---
id: auth-entra
title: Microsoft Entra ID
sidebar_position: 5
---

# Microsoft Entra ID

muvee supports Microsoft Entra ID (formerly Azure AD) via **OpenID Connect** on top of the
**OAuth 2.0 authorization code flow**. It is the only provider wired into **both** planes from a
single set of credentials:

| Plane | Who signs in | Toggle |
|---|---|---|
| Platform | muvee control-panel users (`muvee_session`) | `platform_entra_login_enabled` |
| Project | end users of a project subdomain, through ForwardAuth (`muvee_fwd_session`) | `entra_enabled` |

Unlike Google / Feishu / WeCom / DingTalk, Entra is **not** configured with environment variables:
everything lives in **Admin → Settings** (stored in `system_settings`), so no redeploy is needed.
`ENTRA_*` env vars are only a fallback for values left blank in the UI.

## Setup

### 1. Register the application in Azure

1. In the [Azure portal](https://portal.azure.com/), go to **Microsoft Entra ID → App registrations → New registration**
2. Pick the **Supported account types** that match your intent — this determines the tenant value below
3. Under **Redirect URI**, choose the **Web** platform and add the URIs you need (replace `example.com`
   with your `BASE_DOMAIN`):
   ```
   https://example.com/auth/entra/callback   # platform login
   https://example.com/_oauth/entra          # project subdomains (ForwardAuth)
   ```
   Both can live on the same app registration — add only the ones for the planes you enable.

   :::note Multi-domain
   If you serve muvee under multiple base domains (`BASE_DOMAINS`), add **both** URIs for **each**
   domain — muvee sends the user back to whichever base domain they started on. See
   [Configuration → Multi-domain](../configuration#multi-domain).
   :::
4. Go to **Certificates & secrets → New client secret** and copy the secret **Value** (not its ID).
   Entra secrets expire — note the expiry date, because sign-in breaks on that day.

### 2. Configure muvee

In **Admin → Settings → Social Login Providers**, fill in the **Microsoft Entra ID** card:

| Field | Where it comes from |
|---|---|
| Directory (tenant) ID | Azure app registration **Overview** |
| Application (client) ID | Azure app registration **Overview** |
| Client Secret | the secret **Value** from step 4 |

Then tick the planes you want and press **Save**:

- **Enable on project subdomains** — muvee-server reloads muvee-authservice's provider set
  immediately, so the button shows up on project login pages without a restart.
- **Enable on the muvee platform login page** — registered in-process, also without a restart.

The card shows both redirect URIs read-only, with a copy button, so you can paste them straight into
Azure.

### 3. Pick the projects that offer it

Enabling Entra downstream makes it *available*; each project's **Auth** tab decides what its login
page shows. Leave **Sign-in providers** empty to offer every configured provider, or tick a subset
(now including Microsoft) to restrict it.

## Tenant values and what they imply

`entra_tenant_id` accepts three shapes, and the choice changes how strictly muvee validates tokens:

| Value | Who can sign in | ID-token check | `ALLOWED_DOMAINS` |
|---|---|---|---|
| Directory GUID (recommended) | that one directory | issuer **and** `tid` must equal the configured tenant | skipped (org-scoped) |
| Verified domain (`contoso.onmicrosoft.com`) | that one directory | issuer must match the `tid` it resolves to | skipped (org-scoped) |
| `organizations` / `common` / `consumers` | any work/school directory (plus personal accounts for the latter two) | issuer must match the token's `tid` | **enforced** |

In every case the audience must equal your client ID, and the ID token signature is verified against
the tenant's JWKS at `login.microsoftonline.com`.

Single-tenant configurations are treated as **org-scoped**, the same way Feishu / WeCom / DingTalk
are: membership in the directory is itself the authorisation, so the platform's `ALLOWED_DOMAINS`
email-domain whitelist is skipped. With a multi-tenant alias anyone in the world can complete the
flow, so `ALLOWED_DOMAINS` (and `access_mode`) remain your only gate — set them.

## Claims muvee reads

| Claim | Used for |
|---|---|
| `email`, falling back to `preferred_username` (the UPN) | the platform / project identity |
| `name` | display name |
| `oid` + `tid` | stable subject key |
| `tid`, `iss`, `aud` | validation (see above) |

Entra v2.0 ID tokens carry no `picture` claim, so muvee users signed in this way have no avatar.

Guest accounts that expose neither `email` nor a UPN-shaped `preferred_username` cannot be admitted
to the platform plane — there is no address to match against `ADMIN_EMAILS`, the invite white-list,
or `ALLOWED_DOMAINS`.

## Settings reference

| Setting | Values | Description |
|---|---|---|
| `entra_enabled` | `true` / `false` | Offer Entra on project subdomains |
| `platform_entra_login_enabled` | `true` / `false` | Offer Entra on the muvee platform login page |
| `entra_tenant_id` | GUID / domain / `common` / `organizations` / `consumers` | Azure directory |
| `entra_client_id` | — | Application (client) ID |
| `entra_client_secret` | — | Client secret **value** |

Env fallbacks, used per-field only when the setting is blank: `ENTRA_TENANT_ID`, `ENTRA_CLIENT_ID`,
`ENTRA_CLIENT_SECRET`, plus `ENTRA_REDIRECT_URL` (overrides the derived platform callback) and
`PLATFORM_ENTRA_LOGIN` (fallback for the platform toggle).

Secrets are stored unencrypted in `system_settings`, the same threat model as the other provider
credentials — the admin-only settings API is the protection boundary.

## Troubleshooting

**`AADSTS50011: redirect URI mismatch`** — the URI in Azure must match byte for byte, including
`https://` and the trailing path. Copy it from the settings card rather than typing it.

**The button doesn't appear after saving** — the credentials are incomplete (a blank tenant, client
ID or secret registers nothing on purpose) or the tenant is malformed; check the muvee-server log for
`entra provider`. For project pages also confirm the project's **Sign-in providers** list isn't
restricted to other providers.

**`id_token was issued by tenant …, not the configured …`** — the app registration is multi-tenant
while `entra_tenant_id` pins a single GUID. Either point the tenant at the directory actually issuing
tokens, or switch it to `organizations`.

**Sign-in worked and then stopped** — check whether the client secret expired in Azure.
