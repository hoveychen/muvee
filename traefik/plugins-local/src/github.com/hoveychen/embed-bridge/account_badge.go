package embed_bridge

// sdkAccountBadgeScript is the per-subsite account badge (BACKLOG-20261121-001):
// one small link, bottom-right, to the host's own /_oauth/me account page,
// where the visitor sees which account they are signed in as and can sign out.
//
// The plugin has no switch for it (no config key: io runs an older plugin
// build mounted from the host, see TestConfigHasNoBadgeKey). It only runs when
//
//   - the injected <script> tag carries data-muvee-account, which the plugin
//     sets on top-level page loads that passed ForwardAuth (accountMarkable);
//   - the page is not framed (window.parent check, on top of Sec-Fetch-Dest);
//   - authservice's same-origin /_oauth/account-ui answers {"show": true}
//     (the project's account_badge setting, plus a live session). An older
//     authservice answers 404: no badge, no error.
//
// The answer is cached for five minutes in sessionStorage so a multi-page
// site does not pay a request per page. Nothing about the visitor is written
// into the page — the link text is a fixed word.
//
// Rendering stays out of the tenant's way: a closed Shadow DOM, styles set
// through CSSOM only (el.style.setProperty — a style attribute or <style>
// element would trip a strict style-src CSP), data-html2canvas-ignore so the
// embed selection screenshot skips it, hidden while printing, and a collapse
// button whose state lives in localStorage. Every storage access is wrapped:
// a sandboxed page or blocked storage just means no cache / not remembered.
// Under a strict script CSP (nonce / strict-dynamic) the SDK itself is
// blocked, so there is no badge — the plugin never rewrites a tenant's CSP.
const sdkAccountBadgeScript = `
// muvee account badge — a small link to this host's /_oauth/me.
(function () {
  if (window.__muveeAccountBadge) return;
  window.__muveeAccountBadge = true;
  try {
    if (window.parent !== window) return;
  } catch (e) { return; }
  if (!document.querySelector('script[data-muvee-account]')) return;
  if (typeof window.fetch !== 'function') return;

  var CACHE_KEY = '__muvee_account_ui';
  var COLLAPSED_KEY = '__muvee_account_badge_collapsed';
  var TTL = 300000;

  function cached() {
    try {
      var c = JSON.parse(sessionStorage.getItem(CACHE_KEY) || 'null');
      var age = c ? Date.now() - c.at : -1;
      if (c && typeof c.show === 'boolean' && age >= 0 && age < TTL) return c.show;
    } catch (e) {}
    return null;
  }
  function remember(show) {
    try { sessionStorage.setItem(CACHE_KEY, JSON.stringify({ show: show, at: Date.now() })); } catch (e) {}
  }
  function isCollapsed() {
    try { return localStorage.getItem(COLLAPSED_KEY) === '1'; } catch (e) { return false; }
  }
  function setCollapsed(v) {
    try {
      if (v) localStorage.setItem(COLLAPSED_KEY, '1');
      else localStorage.removeItem(COLLAPSED_KEY);
    } catch (e) {}
  }
  function css(el, props) {
    for (var k in props) {
      if (Object.prototype.hasOwnProperty.call(props, k)) el.style.setProperty(k, props[k]);
    }
  }

  var FONT = '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif';

  function render() {
    if (!document.body || document.querySelector('[data-muvee-account-badge]')) return;
    var host = document.createElement('div');
    if (typeof host.attachShadow !== 'function') return;
    host.setAttribute('data-muvee-account-badge', '');
    host.setAttribute('data-html2canvas-ignore', 'true');
    css(host, { position: 'fixed', right: '12px', bottom: '12px', 'z-index': '2147483000',
      margin: '0', padding: '0', border: '0', width: 'auto', height: 'auto', display: 'block' });
    var root;
    try { root = host.attachShadow({ mode: 'closed' }); } catch (e) { return; }

    var en = !/^zh/i.test(navigator.language || '');
    var tip = en ? 'Your account on this site: who you are signed in as, and sign out'
      : '本站账户：查看当前登录的账号、退出登录';

    var pill = document.createElement('div');
    css(pill, { display: 'flex', 'align-items': 'center', gap: '2px', 'font-family': FONT,
      'font-size': '12px', 'line-height': '1', color: '#fff', background: 'rgba(17, 24, 39, 0.78)',
      'border-radius': '999px', padding: '0 2px 0 10px', 'box-shadow': '0 1px 4px rgba(0, 0, 0, 0.25)' });

    var link = document.createElement('a');
    link.href = '/_oauth/me';
    link.textContent = en ? 'Account' : '账户';
    link.title = tip;
    link.setAttribute('aria-label', tip);
    css(link, { color: '#fff', 'text-decoration': 'none', padding: '6px 2px', 'font-family': FONT,
      'font-size': '12px' });

    var close = document.createElement('button');
    close.type = 'button';
    close.textContent = '×';
    close.title = en ? 'Collapse' : '收起';
    close.setAttribute('aria-label', close.title);
    css(close, { border: '0', background: 'transparent', color: 'rgba(255, 255, 255, 0.75)',
      cursor: 'pointer', 'font-size': '14px', 'line-height': '1', padding: '4px 6px', margin: '0',
      'font-family': FONT });

    var handle = document.createElement('button');
    handle.type = 'button';
    handle.title = en ? 'Show the account link' : '显示账户入口';
    handle.setAttribute('aria-label', handle.title);
    css(handle, { width: '12px', height: '12px', 'border-radius': '999px', border: '0', padding: '0',
      margin: '0', cursor: 'pointer', background: 'rgba(17, 24, 39, 0.45)',
      'box-shadow': '0 1px 3px rgba(0, 0, 0, 0.2)' });

    function apply() {
      var c = isCollapsed();
      css(pill, { display: c ? 'none' : 'flex' });
      css(handle, { display: c ? 'block' : 'none' });
    }
    close.addEventListener('click', function () { setCollapsed(true); apply(); });
    handle.addEventListener('click', function () { setCollapsed(false); apply(); });
    window.addEventListener('beforeprint', function () { css(host, { display: 'none' }); });
    window.addEventListener('afterprint', function () { css(host, { display: 'block' }); });

    pill.appendChild(link);
    pill.appendChild(close);
    root.appendChild(pill);
    root.appendChild(handle);
    apply();
    document.body.appendChild(host);
  }

  function decide() {
    var c = cached();
    if (c !== null) {
      if (c) render();
      return;
    }
    window.fetch('/_oauth/account-ui', { credentials: 'same-origin', cache: 'no-store',
      headers: { Accept: 'application/json' } })
      .then(function (res) {
        if (!res.ok) { remember(false); return null; }
        return res.json();
      })
      .then(function (body) {
        if (!body) return;
        var show = body.show === true;
        remember(show);
        if (show) render();
      })
      .catch(function () {});
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', decide, { once: true });
  } else {
    decide();
  }
})();
`
