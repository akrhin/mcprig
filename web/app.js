/*
  mcprig — панель. Vanilla JS, без зависимостей.
  Данные: /api/state (5 с), выбранный набор — health и /api/calls (5 с), лог — по
  кнопке и 10 с, пока набор запущен. Узлы обновляются на месте (см. sync), поэтому
  обновление не мигает и не сбрасывает прокрутку и фокус.
  ?mock=1 — единственная отладочная ветка: те же URL читаются из web/mock/*.json.
*/
(function () {
  'use strict';

  var MOCK = new URLSearchParams(location.search).get('mock') === '1';
  var $ = function (id) { return document.getElementById(id); };

  // набор из адреса (#/set/demo) выбирается сразу, данные подтягивает первый /api/state
  var linkId = (/^#\/set\/([^/]+)$/.exec(location.hash) || [])[1];
  var st = {
    sets: [], servers: [], sel: linkId ? decodeURIComponent(linkId) : null,
    health: null, calls: null, env: [], logKey: '', errTimer: 0, busy: false, boot: !!linkId
  };

  /* ---------- мелочи ---------- */

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) { n.className = cls; }
    if (text != null) { n.textContent = String(text); }
    return n;
  }

  function fmtN(v) { return v == null || v === '' ? '—' : Number(v).toLocaleString('ru-RU'); }

  function fmtBytes(v) {
    if (v == null) { return '—'; }
    if (v < 10240) { return fmtN(v) + ' Б'; }
    return fmtN(Math.round(v / 1024)) + ' КБ';
  }

  // schemaLine — вес list_commands набора: байты, токены и число инструментов.
  function schemaLine(s) {
    if (!s) { return '—'; }
    return fmtBytes(s.schemaBytes) + ' · ' + fmtN(s.schemaTokens) + ' ток · ' + fmtN(s.tools) + ' инстр.';
  }

  function pct(fail, all) {
    if (!all) { return '—'; }
    return (fail / all * 100).toFixed(1).replace('.', ',') + ' %';
  }

  function mins(n) { return (n < 10 ? '0' : '') + n; }

  function clock(iso) {
    var d = iso ? new Date(iso) : new Date();
    if (isNaN(d.getTime())) { return '—'; }
    return mins(d.getHours()) + ':' + mins(d.getMinutes()) + ':' + mins(d.getSeconds());
  }

  function day(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) { return '—'; }
    return mins(d.getDate()) + '.' + mins(d.getMonth() + 1) + '.' + d.getFullYear();
  }

  function uptime(set) {
    var from = set.runtime && set.runtime.startedAt;
    if (!set.runtime || !set.runtime.running || !from) { return '—'; }
    var s = Math.max(0, (Date.now() - new Date(from).getTime()) / 1000);
    var h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60);
    return h ? h + ' ч ' + mins(m) + ' м' : m + ' м';
  }

  function serversWord(n) {
    if (n % 10 === 1 && n % 100 !== 11) { return 'сервер'; }
    if (n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14)) { return 'сервера'; }
    return 'серверов';
  }

  function setState(set) {
    var r = (set && set.runtime) || {};
    if (r.running) { return { cls: 'ok', text: 'работает' }; }
    if (r.error || (r.exitCode != null && r.exitCode > 0)) { return { cls: 'err', text: 'упал' }; }
    return { cls: 'warn', text: 'остановлен' };
  }

  /* ---------- синхронизация списков ---------- */

  // Обновляет узлы по ключу: существующие правятся на месте, лишние убираются.
  function sync(box, items, key, make, fill) {
    var map = box.__map || (box.__map = new Map());
    var seen = new Set();
    items.forEach(function (item, i) {
      var k = key(item, i);
      seen.add(k);
      var node = map.get(k);
      if (!node) { node = make(item); map.set(k, node); }
      fill(node, item, i);
      var cur = box.children[i];
      if (cur !== node) { box.insertBefore(node, cur || null); }
    });
    map.forEach(function (node, k) {
      if (!seen.has(k)) { box.removeChild(node); map.delete(k); }
    });
  }

  function show(node, on) { node.hidden = !on; }

  /* ---------- сеть ---------- */

  function mockName(path) {
    if (path.indexOf('/api/state') === 0) { return 'state'; }
    if (path.indexOf('/api/env') === 0) { return 'env'; }
    if (/\/servers$/.test(path)) { return 'set-servers'; }
    if (/\/logs/.test(path)) { return 'logs'; }
    if (path.indexOf('/api/calls') === 0) { return 'calls'; }
    return 'state';
  }

  function get(path) {
    var url = MOCK ? 'mock/' + mockName(path) + '.json' : path;
    return fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
      .catch(function () { throw new Error('нет связи с демоном'); })
      .then(function (r) {
        if (r.status === 404) { return null; }
        if (!r.ok) { throw new Error('HTTP ' + r.status + ' · ' + path); }
        return r.json();
      });
  }

  function send(method, path, body) {
    if (MOCK) { return Promise.resolve({ ok: true, ms: 14, tools: [] }); }
    return fetch(path, {
      method: method,
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: body ? JSON.stringify(body) : method === 'POST' ? '{}' : undefined
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (data) {
        if (!r.ok) { throw new Error(data.error || ('HTTP ' + r.status)); }
        return data;
      });
    }).catch(function (e) {
      fail(e.message || String(e));
      throw e;
    });
  }

  /* ---------- плашка ошибки ---------- */

  function fail(msg, err) {
    if (err && window.console) { console.error(err); }
    $('errbar-msg').textContent = msg;
    show($('errbar'), true);
    clearTimeout(st.errTimer);
    st.errTimer = setTimeout(function () { show($('errbar'), false); }, 12000);
  }

  /* ---------- /api/state ---------- */

  function loadState() {
    return get('/api/state').then(function (data) {
      if (!data) { return; }
      st.sets = data.sets || [];
      st.servers = data.servers || [];
      renderTop();
      renderRail();
      renderSetsTable();
      var ids = st.sets.map(function (s) { return s.id; });
      if (ids.indexOf(st.sel) === -1) { select(ids[0] || null); }
      else if (st.boot) { st.boot = false; select(st.sel, true); }
      else { renderHead(); }
    }).catch(function (e) { fail(e.message, e); });
  }

  function renderTop() {
    var running = st.sets.filter(function (s) { return s.runtime && s.runtime.running; }).length;
    $('c-sets').textContent = fmtN(st.sets.length);
    $('c-servers').textContent = fmtN(st.servers.length);
    $('f-sets').textContent = fmtN(st.sets.length);
    $('f-servers').textContent = fmtN(st.servers.length);
    $('f-running').textContent = fmtN(running);
    $('f-updated').textContent = 'обновлено ' + clock();
    $('rail-count').textContent = fmtN(st.sets.length);
    $('sets-count').textContent = fmtN(st.sets.length);

    var none = st.sets.length === 0;
    show($('empty-state'), none);
    show($('rail-empty'), none);
    show($('phead'), !none);
    show($('sets-block'), !none);
    show($('calls-block'), !none);
    show($('kpis'), !none);
    if (none) { renderKpis(null); renderCalls(null); renderServers(null); renderLog([]); }
  }

  function renderRail() {
    sync($('rail-sets'), st.sets, function (s) { return s.id; },
      function () {
        var a = el('a', 'setrow');
        a.appendChild(el('span', 'sname')).appendChild(el('span', 'dot'));
        a.firstChild.appendChild(el('span', 'od-truncate'));
        a.appendChild(el('span', 'sport n'));
        a.appendChild(el('span', 'srate'));
        return a;
      },
      function (a, s) {
        var state = setState(s);
        a.href = '#/set/' + s.id;
        a.dataset.id = s.id;
        a.classList.toggle('is-active', s.id === st.sel);
        if (s.id === st.sel) { a.setAttribute('aria-current', 'true'); } else { a.removeAttribute('aria-current'); }
        a.querySelector('.dot').className = 'dot ' + state.cls;
        a.querySelector('.od-truncate').textContent = s.name || s.id;
        a.querySelector('.sport').textContent = s.port;
        var rate = a.querySelector('.srate');
        rate.replaceChildren();
        if (state.cls === 'ok') {
          rate.appendChild(el('span', 'n', (s.servers || []).length + ' ' + serversWord((s.servers || []).length)));
        } else {
          rate.textContent = state.text;
        }
        a.title = (s.description || s.id) + ' · порт ' + s.port;
      });
  }

  function aggregated() {
    var c = (st.calls && st.calls.counters) || [];
    var all = { calls: 0, failures: 0, p50: null, p95: null };
    c.forEach(function (row) {
      all.calls += row.calls || 0;
      all.failures += row.failures || 0;
      if (row.p50ms != null) { all.p50 = Math.max(all.p50 || 0, row.p50ms); }
      if (row.p95ms != null) { all.p95 = Math.max(all.p95 || 0, row.p95ms); }
    });
    return all;
  }

  function renderSetsTable() {
    var agg = aggregated();
    sync($('sets-body'), st.sets, function (s) { return s.id; },
      function () {
        var tr = el('tr');
        var name = tr.appendChild(el('td', 'tb-name'));
        tr.appendChild(el('td', 'portcell'));
        var cell = tr.appendChild(el('td'));
        var pill = cell.appendChild(el('span', 'state'));
        pill.appendChild(el('span', 'dot'));
        pill.appendChild(document.createTextNode(' '));
        pill.appendChild(el('span'));
        tr.appendChild(el('td', 'num'));
        tr.appendChild(el('td', 'n'));
        tr.appendChild(el('td', 'num'));
        tr.appendChild(el('td', 'num'));
        return tr;
      },
      function (tr, s) {
        var state = setState(s);
        var pill = tr.children[2].querySelector('.state');
        tr.classList.toggle('row-warn', state.cls !== 'ok');
        tr.children[0].textContent = s.name || s.id;
        tr.children[1].textContent = s.port;
        pill.querySelector('.dot').className = 'dot ' + state.cls;
        pill.lastChild.textContent = state.text;
        tr.children[3].textContent = (s.servers || []).length;
        tr.children[4].textContent = uptime(s);
        var mine = s.id === st.sel && st.calls;
        tr.children[5].textContent = mine ? fmtN(agg.calls) : '—';
        tr.children[6].textContent = mine ? pct(agg.failures, agg.calls) : '—';
      });
  }

  /* ---------- выбранный набор ---------- */

  function current() {
    for (var i = 0; i < st.sets.length; i++) {
      if (st.sets[i].id === st.sel) { return st.sets[i]; }
    }
    return null;
  }

  function select(id, force) {
    if (id === st.sel && !force) { renderHead(); return; }
    st.sel = id;
    st.health = null;
    st.calls = null;
    st.logKey = '';
    if (id) {
      history.replaceState(null, '', '#/set/' + id);
      document.title = 'mcprig — набор ' + id;
    } else {
      history.replaceState(null, '', location.pathname);
      document.title = 'mcprig — панель';
    }
    renderRail();
    renderHead();
    renderServers(null);
    renderCalls(null);
    renderLog([]);
    if (id) { loadHealth(); loadCalls(); loadLog(); }
  }

  function renderHead() {
    var set = current();
    if (!set) {
      show($('set-error'), false);
      $('k-calls').textContent = $('k-p50').textContent = $('k-p95').textContent = $('k-err').textContent = '—';
      $('schema').textContent = '—';
      return;
    }
    var state = setState(set);
    $('set-name').textContent = 'Набор ' + (set.name || set.id);
    $('set-port').textContent = ':' + set.port;
    var pill = $('set-state');
    pill.replaceChildren(el('span', 'dot ' + state.cls), document.createTextNode(state.text));
    $('endpoint').textContent = 'http://' + (location.hostname || '127.0.0.1') + ':' + set.port + '/mcp';
    $('key').textContent = set.keyPrefix ? set.keyPrefix + '…' : (set.open ? 'набор открыт' : 'ключа нет');
    $('schema').textContent = schemaLine(st.calls && st.calls.schema);
    $('run').textContent = set.runtime && set.runtime.running ? 'Перезапустить' : 'Запустить';
    $('dlg-set').textContent = set.name || set.id;

    var r = set.runtime || {};
    var bad = !r.running && (r.error || (r.exitCode != null && r.exitCode > 0));
    show($('set-error'), !!bad);
    if (bad) {
      $('set-error-h').textContent = r.error ? 'набор остановлен' : 'процесс завершился (code ' + r.exitCode + ')';
      var lines = [];
      if (r.error) { lines.push(r.error); }
      if (r.exitCode != null) { lines.push('код выхода ' + r.exitCode); }
      if (r.restarts) { lines.push('автоперезапусков: ' + r.restarts); }
      $('set-error-d').textContent = lines.join('\n');
    }
  }

  function renderKpis(data) {
    var agg = data ? aggregated() : null;
    $('k-calls').textContent = agg ? fmtN(agg.calls) : '—';
    $('k-p50').textContent = agg && agg.p50 != null ? agg.p50 + ' мс' : '—';
    $('k-p95').textContent = agg && agg.p95 != null ? agg.p95 + ' мс' : '—';
    $('k-err').textContent = agg ? pct(agg.failures, agg.calls) : '—';
    $('c-calls').textContent = agg ? fmtN(agg.calls) : '—';
    $('c-err').textContent = agg ? pct(agg.failures, agg.calls) : '—';
  }

  function loadHealth() {
    if (!st.sel) { return; }
    var id = st.sel;
    return get('/api/sets/' + id + '/servers').then(function (data) {
      if (id !== st.sel) { return; }
      st.health = data;
      renderServers(data);
    }).catch(function (e) { fail(e.message, e); });
  }

  function renderServers(health) {
    var set = current();
    var names = (set && set.servers) || [];
    var rows = names.map(function (name) {
      var h = null;
      if (health && health.servers) {
        h = health.servers.filter(function (s) { return s.name === name; })[0] || null;
      }
      return { name: name, health: h };
    });
    $('srv-count').textContent = fmtN(rows.length);
    show($('servers-empty'), rows.length === 0);
    sync($('servers'), rows, function (r) { return r.name; },
      function () {
        var li = el('li', 'srv');
        var top = li.appendChild(el('div', 'srv-top'));
        top.appendChild(el('span', 'dot'));
        top.appendChild(el('span', 'snm od-truncate'));
        top.appendChild(el('span', 'badge'));
        var probe = top.appendChild(el('button', 'mini', 'проверить'));
        probe.type = 'button';
        li.appendChild(el('div', 'srv-note'));
        return li;
      },
      function (li, row) {
        var srv = st.servers.filter(function (s) { return s.name === row.name; })[0];
        var h = row.health;
        var cls = 'warn';
        var note = health && !health.running ? 'набор не запущен' : 'не проверялся';
        if (h && h.healthy === true) { cls = 'ok'; note = 'health ok'; }
        if (h && h.healthy === false) { cls = 'err'; note = h.error || 'нет ответа'; }
        li.querySelector('.dot').className = 'dot ' + cls;
        li.querySelector('.snm').textContent = row.name;
        var kind = (srv && srv.transport) || (srv && srv.command ? 'stdio' : srv && srv.url ? 'http' : '—');
        li.querySelector('.badge').textContent = kind;
        var n = li.querySelector('.srv-note');
        n.textContent = note;
        n.classList.toggle('is-err', cls === 'err');
        li.querySelector('.mini').dataset.server = row.name;
      });
  }

  function loadCalls() {
    if (!st.sel) { return; }
    var id = st.sel;
    return get('/api/calls?set=' + encodeURIComponent(id) + '&limit=50').then(function (data) {
      if (id !== st.sel) { return; }
      st.calls = data;
      renderKpis(st.calls);
      renderCalls(st.calls);
      renderSetsTable();
      $('schema').textContent = schemaLine(data.schema);
    }).catch(function (e) { fail(e.message, e); });
  }

  function renderCalls(data) {
    var tail = (data && data.calls) || [];
    if (!data) {
      renderKpis(null);
      sync($('calls-body'), [], function () { return ''; }, function () { return el('tr'); }, function () {});
      $('calls-count').textContent = '0';
      show($('calls-note'), true);
      $('calls-note').textContent = 'метрики вызовов недоступны: роут /api/calls ещё не поднят';
      return;
    }
    show($('calls-note'), tail.length === 0);
    $('calls-note').textContent = 'вызовов пока нет';
    sync($('calls-body'), tail, function (c, i) { return i; },
      function () {
        var tr = el('tr');
        tr.appendChild(el('td', 'n'));
        tr.appendChild(el('td'));
        tr.appendChild(el('td', 'n'));
        tr.appendChild(el('td'));
        tr.appendChild(el('td', 'num n'));
        tr.appendChild(el('td', 'num st n'));
        return tr;
      },
      function (tr, c) {
        tr.children[0].textContent = clock(c.time);
        tr.children[1].textContent = c.server || '—';
        tr.children[2].textContent = c.tool || '—';
        tr.children[3].textContent = c.client || '—';
        tr.children[4].className = 'num n' + (c.ms > 1000 ? ' slow' : '');
        tr.children[4].textContent = fmtN(c.ms);
        tr.children[5].className = 'num st n' + (c.failed ? ' bad' : '');
        tr.children[5].textContent = c.failed ? 'ошибка' : '200';
        tr.children[5].title = c.error || '';
      });
    filterCalls();
  }

  function loadEnv() {
    return get('/api/env').then(function (data) {
      if (!data) { return; }
      st.env = data.vars || [];
      $('env-count').textContent = fmtN(st.env.length);
      show($('env-empty'), st.env.length === 0);
      sync($('env'), st.env, function (v) { return v.name; },
        function () {
          var li = el('li', 'var');
          var top = li.appendChild(el('div', 'var-top'));
          top.appendChild(el('code'));
          top.appendChild(el('span', 'badge'));
          li.appendChild(el('div', 'var-val n'));
          return li;
        },
        function (li, v) {
          li.querySelector('code').textContent = v.name;
          var badge = li.querySelector('.badge');
          badge.textContent = v.source || '';
          badge.hidden = !v.source;
          li.querySelector('.var-val').textContent = v.set ? 'значение задано' : 'не задана';
          if (data.remoteError && !v.set) { li.querySelector('.var-val').textContent = 'нет значения · ' + data.remoteError; }
        });
    }).catch(function (e) { fail(e.message, e); });
  }

  function loadLog() {
    if (!st.sel) { return; }
    var id = st.sel;
    return get('/api/sets/' + id + '/logs?lines=200').then(function (data) {
      if (id !== st.sel) { return; }
      renderLog((data && data.logs) || []);
    }).catch(function (e) { fail(e.message, e); });
  }

  function renderLog(lines) {
    var key = lines.join('\n');
    if (key === st.logKey) { return; }
    st.logKey = key;
    $('log-count').textContent = fmtN(lines.length);
    show($('log'), lines.length > 0);
    show($('log-empty'), lines.length === 0);
    sync($('log'), lines, function (l, i) { return String(i); },
      function () {
        var row = el('div', 'logline raw');
        row.appendChild(el('span', 'lm'));
        return row;
      },
      function (row, line) {
        var cls = /\b(error|err|fatal|panic)\b/i.test(line) ? 'error' : /\bwarn(ing)?\b/i.test(line) ? 'warn' : '';
        var lm = row.firstChild;
        lm.textContent = line;
        lm.title = line;
        lm.className = 'lm' + (cls ? ' is-' + cls : '');
      });
  }

  /* ---------- поиск по хвосту ---------- */

  function filterCalls() {
    var term = ($('q').value || '').trim().toLowerCase();
    var box = $('calls-body');
    var shown = 0;
    for (var i = 0; i < box.children.length; i++) {
      var row = box.children[i];
      var hit = !term || row.textContent.toLowerCase().indexOf(term) !== -1;
      row.hidden = !hit;
      if (hit) { shown++; }
    }
    var total = box.children.length;
    $('calls-count').textContent = term ? shown + ' из ' + fmtN(total) : fmtN(total);
    if (total && !shown) {
      show($('calls-note'), true);
      $('calls-note').textContent = 'нет вызовов по запросу «' + $('q').value.trim() + '»';
    } else if (total) {
      show($('calls-note'), false);
    }
  }

  /* ---------- действия ---------- */

  function act(btn, path, done) {
    if (st.busy) { return; }
    st.busy = true;
    var label = btn.textContent;
    btn.disabled = true;
    btn.textContent = '…';
    send('POST', path).then(function () {
      return done ? done() : null;
    }).catch(function () {
      /* плашка уже показана */
    }).then(function () {
      st.busy = false;
      btn.disabled = false;
      btn.textContent = label;
    });
  }

  function openKeys() {
    var set = current();
    if (!set) { return; }
    renderKeys(set);
    var dlg = $('keys-dlg');
    if (typeof dlg.showModal === 'function') { dlg.showModal(); }
  }

  function renderKeys(set) {
    var keys = set.keys || [];
    sync($('keys-body'), keys, function (k) { return k.id; },
      function () {
        var tr = el('tr');
        tr.appendChild(el('td', 'n'));
        tr.appendChild(el('td', 'n'));
        tr.appendChild(el('td'));
        tr.appendChild(el('td'));
        var td = tr.appendChild(el('td', 'num'));
        var b = td.appendChild(el('button', 'mini', 'отозвать'));
        b.type = 'button';
        return tr;
      },
      function (tr, k) {
        var revoked = !!k.revokedAt;
        tr.children[0].textContent = (set.keyPrefix || '—') + '…';
        tr.children[1].textContent = day(k.createdAt);
        tr.children[2].textContent = revoked ? 'отозван ' + day(k.revokedAt) : 'действует';
        tr.children[2].className = revoked ? 'st' : '';
        tr.children[3].textContent = k.note || '—';
        var btn = tr.children[4].querySelector('button');
        btn.dataset.key = k.id;
        btn.hidden = revoked;
        tr.classList.toggle('row-warn', revoked);
      });
  }

  /* ---------- привязки ---------- */

  $('rail-sets').addEventListener('click', function (e) {
    var row = e.target.closest('.setrow');
    if (!row) { return; }
    e.preventDefault();
    if (row.dataset.id !== st.sel) { select(row.dataset.id); }
  });

  window.addEventListener('hashchange', function () {
    var m = /^#\/set\/([^/]+)$/.exec(location.hash);
    if (m && m[1] !== st.sel) { select(decodeURIComponent(m[1])); }
  });

  $('q').addEventListener('input', filterCalls);
  document.addEventListener('keydown', function (e) {
    var tag = document.activeElement && document.activeElement.tagName;
    if (e.key === '/' && tag !== 'INPUT' && tag !== 'TEXTAREA') {
      e.preventDefault();
      $('q').focus();
    }
  });

  $('dim').addEventListener('click', function () {
    var on = this.getAttribute('aria-pressed') === 'true';
    this.setAttribute('aria-pressed', String(!on));
    document.documentElement.classList.toggle('dim', !on);
  });

  $('copy').addEventListener('click', function () {
    var btn = this;
    var text = $('endpoint').textContent;
    var done = function (ok) {
      btn.textContent = ok ? 'скопировано' : 'не удалось';
      setTimeout(function () { btn.textContent = 'копировать'; }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { done(true); }, function () { done(false); });
    } else {
      done(false);
    }
  });

  $('run').addEventListener('click', function () {
    var set = current();
    if (!set) { return; }
    var running = set.runtime && set.runtime.running;
    act(this, '/api/sets/' + set.id + '/' + (running ? 'restart' : 'start'), function () {
      return loadState().then(loadHealth);
    });
  });

  $('log-refresh').addEventListener('click', function () { loadLog(); });
  $('errbar-close').addEventListener('click', function () { show($('errbar'), false); });

  $('servers').addEventListener('click', function (e) {
    var btn = e.target.closest('button[data-server]');
    if (!btn) { return; }
    var note = btn.closest('.srv').querySelector('.srv-note');
    note.textContent = 'проверка…';
    note.classList.remove('is-err');
    send('POST', '/api/servers/' + encodeURIComponent(btn.dataset.server) + '/probe').then(function (r) {
      note.textContent = r.ok
        ? 'проверка ok · ' + r.ms + ' мс · инструментов ' + (r.tools || []).length
        : (r.error || 'нет ответа') + ' · ' + r.ms + ' мс';
      note.classList.toggle('is-err', !r.ok);
    }).catch(function () { note.textContent = 'проверка не удалась'; note.classList.add('is-err'); });
  });

  var form = $('addform');
  $('add-toggle').addEventListener('click', function () {
    form.hidden = !form.hidden;
    $('add-err').hidden = true;
    if (!form.hidden) { form.querySelector('input[name="name"]').focus(); }
  });
  $('empty-add').addEventListener('click', function () {
    form.hidden = false;
    form.querySelector('input[name="name"]').focus();
  });
  $('add-cancel').addEventListener('click', function () {
    form.hidden = true;
    $('add-err').hidden = true;
  });
  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var name = form.querySelector('input[name="name"]').value.trim();
    var portRaw = form.querySelector('input[name="port"]').value.trim();
    var err = $('add-err');
    if (!name) { err.textContent = 'имя обязательно'; err.hidden = false; return; }
    var body = { name: name };
    if (portRaw) { body.port = parseInt(portRaw, 10); }
    send('POST', '/api/sets', body).then(function (set) {
      form.reset();
      form.hidden = true;
      err.hidden = true;
      return loadState().then(function () { if (set && set.id) { select(set.id); } });
    }).catch(function (ex) {
      err.textContent = ex.message;
      err.hidden = false;
    });
  });

  $('keys').addEventListener('click', openKeys);
  $('keys-close').addEventListener('click', function () { $('keys-dlg').close(); });
  $('keys-done').addEventListener('click', function () { $('keys-dlg').close(); });
  $('keys-issue').addEventListener('click', function () {
    var set = current();
    if (!set) { return; }
    act(this, '/api/sets/' + set.id + '/keys', function () {
      return loadState().then(function () { renderKeys(current() || set); });
    });
  });
  $('keys-body').addEventListener('click', function (e) {
    var btn = e.target.closest('button[data-key]');
    var set = current();
    if (!btn || !set) { return; }
    btn.disabled = true;
    send('DELETE', '/api/sets/' + set.id + '/keys/' + encodeURIComponent(btn.dataset.key)).then(function () {
      return loadState().then(function () { renderKeys(current() || set); });
    }).catch(function () { btn.disabled = false; });
  });

  /* ---------- цикл обновления ---------- */

  var timers = [];
  function every(ms, fn) { timers.push(setInterval(function () { if (!document.hidden) { fn(); } }, ms)); }

  loadEnv();
  loadState().then(function () {
    every(5000, loadState);
    every(5000, function () { if (st.sel) { loadHealth(); loadCalls(); } });
    every(10000, function () {
      var set = current();
      if (set && set.runtime && set.runtime.running) { loadLog(); }
    });
    every(30000, loadEnv);
  });
})();
