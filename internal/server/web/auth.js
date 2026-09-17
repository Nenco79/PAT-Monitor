// Script shared by the login and first-configuration pages.
//
// It lives in an external file and not inline in the HTML: the
// Content-Security-Policy forbids inline scripts (default-src 'self'), so a
// <script> block in the page would not be executed. When that happened, the
// browser fell back on the form's native submission and, with no method="post",
// sent the password in the query string.
'use strict';

const form = document.getElementById('form');
const msg = document.getElementById('msg');
const submit = document.getElementById('submit');
const mode = form.dataset.mode; // "login" or "setup"

function show(text, kind) {
  msg.textContent = text;
  msg.className = 'msg show ' + kind;
}

form.addEventListener('submit', async (e) => {
  // From here on the request is handled by fetch: the native submission, which
  // would reload the page, is prevented.
  e.preventDefault();

  const password = document.getElementById('password').value;
  const confirm = mode === 'setup' ? document.getElementById('confirm').value : '';

  msg.className = 'msg';
  if (mode === 'setup' && password !== confirm) {
    show(T('auth.mismatch'), 'error');
    return;
  }

  submit.disabled = true;
  try {
    const res = await fetch(form.action, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(mode === 'setup' ? {password, confirm} : {password}),
    });

    if (res.ok) {
      if (mode === 'setup') {
        show(T('auth.set'), 'ok');
        setTimeout(() => location.href = '/login', 700);
      } else {
        location.href = '/';
      }
      return;
    }

    const body = await res.json().catch(() => ({}));
    show(TErr(body.error, body.retryAfter), 'error');
  } catch (err) {
    show(T('auth.unreachable'), 'error');
  } finally {
    submit.disabled = false;
    const field = document.getElementById('password');
    if (field) field.select();
  }
});

// Shows any error handed back by the fallback without JavaScript.
const params = new URLSearchParams(location.search);
if (params.has('error')) {
  show(TErr(params.get('error'), params.get('retryAfter')), 'error');
  // Cleans the address: the error must not stay in the history.
  history.replaceState(null, '', location.pathname);
}
