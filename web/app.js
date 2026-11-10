/*
  mcprig — панель. Vanilla JS, без зависимостей.
  Данные: /api/state (5 с), выбранный набор — health и /api/calls (5 с), сводка по
  всем запущенным наборам для раздела «Статистика» — /api/calls без set (15 с), лог —
  по кнопке и 10 с, пока набор запущен.
  Каркас: три региона со своим скроллом (рельс, основная область, панель деталей),
  поэтому страница не растягивается и горизонтальной прокрутки у окна нет.
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
    health: null, calls: null, env: [], logKey: '', errTimer: 0, busy: false, boot: !!linkId,
    tab: 'overview', sideTab: 'servers', railPref: '',
    st: { period: 'today', set: '', server: '', tool: '', client: '', seq: 0, data: null, facets: null, facetKey: '' }
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

  function hm(ms) {
    var d = new Date(ms);
    return mins(d.getHours()) + ':' + mins(d.getMinutes());
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

  // pctl — перцентиль по уже отсортированному массиву чисел.
  function pctl(sorted, p) {
    if (!sorted.length) { return null; }
    var i = (sorted.length - 1) * p;
    var lo = Math.floor(i), hi = Math.ceil(i);
    if (lo === hi) { return sorted[lo]; }
    return Math.round(sorted[lo] + (sorted[hi] - sorted[lo]) * (i - lo));
  }

  function ms(v) { return v == null ? '—' : fmtN(v) + ' мс'; }

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

  function show(node, on) { if (node) { node.hidden = !on; } }

  /* ---------- сеть ---------- */

  function mockName(path) {
    if (path.indexOf('/api/state') === 0) { return 'state'; }
    if (path.indexOf('/api/env') === 0) { return 'env'; }
    // у каждого набора свой health: иначе статусы соседей переезжают между наборами
    var m = /^\/api\/sets\/([^/]+)\/servers$/.exec(path);
    if (m) { return 'set-' + m[1] + '-servers'; }
    if (/\/logs/.test(path)) { return 'logs'; }
    if (path.indexOf('/api/calls') === 0) { return 'calls'; }
    // срез статистики мок отдаёт по периоду: набор и фильтры файл не меняют
    if (path.indexOf('/api/stats') === 0) {
      var p = /[?&]period=([a-z0-9]+)/.exec(path);
      return 'stats-' + (p ? p[1] : 'today');
    }
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
    var bar = $('errbar');
    if (!bar) { return; }
    $('errbar-msg').textContent = msg;
    show(bar, true);
    clearTimeout(st.errTimer);
    st.errTimer = setTimeout(function () { show(bar, false); }, 12000);
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
      fillFilters();
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
    $('c-running').textContent = fmtN(running);
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
    show($('tabs'), !none);
    show($('region'), !none);
    show($('sets-block'), !none);
    show($('kpis'), !none);
    if (none) { renderKpis(null); renderServers(null); renderLog([]); }
  }

  function renderRail() {
    sync($('rail-sets'), st.sets, function (s) { return s.id; },
      function () {
        var a = el('a', 'setrow');
        a.appendChild(el('span', 'dot'));
        var body = a.appendChild(el('span', 'sbody'));
        body.appendChild(el('span', 'sname od-truncate'));
        var meta = body.appendChild(el('span', 'smeta'));
        meta.appendChild(el('span', 'sport n'));
        meta.appendChild(el('span', 'srate'));
        return a;
      },
      function (a, s) {
        var state = setState(s);
        var name = s.name || s.id;
        a.href = '#/set/' + s.id;
        a.dataset.id = s.id;
        a.classList.toggle('is-active', s.id === st.sel);
        if (s.id === st.sel) { a.setAttribute('aria-current', 'true'); } else { a.removeAttribute('aria-current'); }
        a.querySelector('.dot').className = 'dot ' + state.cls;
        a.querySelector('.sname').textContent = name;
        a.querySelector('.sport').textContent = ':' + s.port;
        var rate = a.querySelector('.srate');
        if (state.cls === 'ok') {
          var n = (s.servers || []).length;
          rate.textContent = n + ' ' + serversWord(n);
        } else {
          rate.textContent = state.text;
        }
        var names = s.servers || [];
        a.title = name + ' · порт ' + s.port + ' · ' + state.text + (names.length ? '\n' + names.join(', ') : '');
        a.setAttribute('aria-label', name + ' · порт ' + s.port + ' · ' + state.text);
      });
  }

  // Вызовы и ошибки выбранного набора: счётчики живут в процессе набора, поэтому
  // у чужих наборов в таблице прочерк — меряем только открытый.
  function aggregated(data) {
    var c = ((data || st.calls) && (data || st.calls).counters) || [];
    var all = { calls: 0, failures: 0 };
    c.forEach(function (row) {
      all.calls += row.calls || 0;
      all.failures += row.failures || 0;
    });
    return all;
  }

  // Перцентили меряем по хвосту вызовов: у счётчиков они по каждому срезу отдельно
  // и складываются в общий только взвешенно, а это уже не тот перцентиль.
  function tailPercentiles(rows) {
    var sorted = rows.map(function (r) { return Number(r.ms) || 0; }).sort(function (a, b) { return a - b; });
    return { p50: pctl(sorted, 0.5), p95: pctl(sorted, 0.95) };
  }

  // Состав набора — одной строкой под именем: плашки переносились и раздували
  // строку таблицы на узкой основной колонке.
  function srvsLine(box, names) {
    box.textContent = names.length ? names.join(' · ') : 'серверов нет';
  }

  function renderSetsTable() {
    var agg = aggregated();
    sync($('sets-body'), st.sets, function (s) { return s.id; },
      function () {
        var tr = el('tr', 'is-click');
        var name = tr.appendChild(el('td', 'tb-name'));
        name.appendChild(el('span', 'setlabel'));
        name.appendChild(el('span', 'srvs'));
        tr.appendChild(el('td', 'portcell hide-sm'));
        var cell = tr.appendChild(el('td'));
        var pill = cell.appendChild(el('span', 'state'));
        pill.appendChild(el('span', 'dot'));
        pill.appendChild(document.createTextNode(' '));
        pill.appendChild(el('span'));
        tr.appendChild(el('td', 'num hide-sm'));
        tr.appendChild(el('td', 'n hide-md'));
        tr.appendChild(el('td', 'num'));
        tr.appendChild(el('td', 'num'));
        return tr;
      },
      function (tr, s) {
        var state = setState(s);
        var pill = tr.children[2].querySelector('.state');
        tr.classList.toggle('row-warn', state.cls !== 'ok');
        var cell = tr.children[0];
        var names = s.servers || [];
        cell.firstChild.textContent = s.name || s.id;
        srvsLine(cell.querySelector('.srvs'), names);
        cell.title = names.length ? names.join(', ') : 'серверов нет';
        tr.children[1].textContent = s.port;
        pill.querySelector('.dot').className = 'dot ' + state.cls;
        pill.lastChild.textContent = state.text;
        tr.children[3].textContent = names.length;
        tr.children[4].textContent = uptime(s);
        var mine = s.id === st.sel && st.calls;
        var g = mine ? agg : null;
        tr.children[5].textContent = g ? fmtN(g.calls) : '—';
        tr.children[6].textContent = g ? pct(g.failures, g.calls) : '—';
        tr.dataset.id = s.id;
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
      $('k-calls').textContent = $('k-err').textContent = $('k-p50').textContent = $('k-p95').textContent = '—';
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
    $('schema').title = 'сколько контекста стоит перечень инструментов набора';
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
    var pctls = data ? tailPercentiles(data.calls || []) : { p50: null, p95: null };
    $('k-calls').textContent = agg ? fmtN(agg.calls) : '—';
    $('k-err').textContent = agg ? pct(agg.failures, agg.calls) : '—';
    $('k-p50').textContent = ms(pctls.p50);
    $('k-p95').textContent = ms(pctls.p95);
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
        top.appendChild(el('span', 'snm'));
        top.appendChild(el('span', 'badge'));
        var foot = li.appendChild(el('div', 'srv-foot'));
        foot.appendChild(el('span', 'srv-note'));
        var probe = foot.appendChild(el('button', 'mini', 'проверить'));
        probe.type = 'button';
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
        li.querySelector('.snm').title = row.name;
        var kind = (srv && srv.transport) || (srv && srv.command ? 'stdio' : srv && srv.url ? 'http' : '—');
        li.querySelector('.badge').textContent = kind;
        var n = li.querySelector('.srv-note');
        n.textContent = note;
        n.title = note;
        n.classList.toggle('is-err', cls === 'err');
        li.querySelector('.mini').dataset.server = row.name;
      });
  }

  function loadCalls() {
    if (!st.sel) { return; }
    var id = st.sel;
    return get('/api/calls?set=' + encodeURIComponent(id) + '&limit=100').then(function (data) {
      if (id !== st.sel) { return; }
      // ответ по одному набору строк `set` не несёт; мок отдаёт общий файл — сужаем сами
      if (data) {
        var mine = function (r) { return !r.set || r.set === id; };
        data.calls = (data.calls || []).filter(mine);
        data.counters = (data.counters || []).filter(mine);
      }
      st.calls = data;
      renderKpis(st.calls);
      renderCalls(st.calls);
      renderTail(st.calls);
      renderSetsTable();
      $('schema').textContent = schemaLine(data && data.schema);
    }).catch(function (e) { fail(e.message, e); });
  }

  /* ---------- вкладка «Вызовы» ---------- */

  // Восемь последних вызовов для «Обзора»: главное, что происходит сейчас,
  // не открывая отдельную вкладку.
  function renderTail(data) {
    var body = $('tail-body');
    var block = $('tail-block');
    show(block, !!data);
    if (!data) { return; }
    var rows = (data.calls || []).slice(0, 8);
    show($('tail-empty'), rows.length === 0);
    $('tail-span').textContent = data.calls && data.calls.length ? 'последние ' + fmtN(Math.min(8, data.calls.length)) : '—';
    var frag = document.createDocumentFragment();
    rows.forEach(function (c) {
      var tr = el('tr', 'is-click');
      tr.appendChild(el('td', 'n', clock(c.time)));
      tr.appendChild(el('td', 'hide-xs', c.server || '—'));
      var tool = tr.appendChild(el('td', 'mono'));
      tool.appendChild(el('span', 'od-truncate', c.tool || '—'));
      tool.title = c.tool || '';
      var t = tr.appendChild(el('td', 'num n hide-sm' + (c.ms > 1000 ? ' slow' : ''), fmtN(c.ms)));
      t.title = c.client || '';
      var stc = tr.appendChild(el('td', 'num st n' + (c.failed ? ' bad' : ''), c.failed ? 'ошибка' : 'ok'));
      stc.title = c.error || c.client || '';
      tr.addEventListener('click', function () { setTab('calls'); });
      frag.appendChild(tr);
    });
    body.replaceChildren(frag);
  }

  function renderCalls(data) {
    var box = $('calls-body');
    var scroll = box.closest('.tw');
    var keep = scroll ? scroll.scrollTop : 0;

    if (!data) {
      box.replaceChildren();
      $('calls-count').textContent = '0';
      show($('calls-note'), true);
      $('calls-note').textContent = 'метрики вызовов недоступны: роут /api/calls ещё не поднят';
      return;
    }

    var term = ($('q').value || '').trim().toLowerCase();
    var onlyErr = $('only-err').checked;
    var all = data.calls || [];
    var rows = all.filter(function (c) {
      if (onlyErr && !c.failed) { return false; }
      if (!term) { return true; }
      return (c.server + ' ' + c.tool + ' ' + c.client).toLowerCase().indexOf(term) !== -1;
    });

    var frag = document.createDocumentFragment();
    rows.forEach(function (c) {
      var tr = el('tr');
      tr.appendChild(el('td', 'n', clock(c.time)));
      tr.appendChild(el('td', '', c.server || '—'));
      tr.appendChild(el('td', 'mono', c.tool || '—'));
      tr.appendChild(el('td', 'hide-md', c.client || '—'));
      var t = tr.appendChild(el('td', 'num n' + (c.ms > 1000 ? ' slow' : ''), fmtN(c.ms)));
      t.classList.add('hide-xs');
      var stc = tr.appendChild(el('td', 'num st n' + (c.failed ? ' bad' : ''), c.failed ? 'ошибка' : 'ok'));
      stc.title = c.error || '';
      frag.appendChild(tr);
    });
    box.replaceChildren(frag);
    if (scroll) { scroll.scrollTop = keep; }

    $('calls-count').textContent = rows.length === all.length ? fmtN(all.length) : rows.length + ' из ' + fmtN(all.length);
    var empty = rows.length === 0;
    show($('calls-note'), empty);
    $('calls-note').textContent = !all.length
      ? 'вызовов пока нет — набор ещё не получал запросов'
      : 'ничего не нашлось — ослабьте фильтр';
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
          li.querySelector('code').title = v.name;
          var badge = li.querySelector('.badge');
          badge.textContent = v.source || '';
          badge.hidden = !v.source;
          var val = li.querySelector('.var-val');
          val.textContent = v.set
            ? 'задана' + (v.source ? ' · ' + v.source : '')
            : 'не задана' + (data.remoteError ? ' · ' + data.remoteError : '');
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
      function () { return el('div', 'logline'); },
      function (row, line) {
        var cls = /\b(error|err|fatal|panic)\b/i.test(line) ? 'is-error' : /\bwarn(ing)?\b/i.test(line) ? 'is-warn' : '';
        row.className = 'logline' + (cls ? ' ' + cls : '');
        row.textContent = line;
      });
  }

  /* ---------- статистика ----------
     Считает бэкенд (/api/stats), панель только просит срез и рисует его.
     Период живёт в адресе (#/stats/7d), поэтому срез можно переслать ссылкой. */

  var PERIODS = ['today', '7d', '30d', 'archive'];
  var PERIOD_NAMES = { today: 'Сегодня', '7d': '7 дней', '30d': '30 дней', archive: 'Архив' };

  function statsPath(filters) {
    var p = '/api/stats?set=' + encodeURIComponent(st.st.set || 'all') +
      '&period=' + st.st.period + '&bucket=' + (st.st.period === 'today' ? 'hour' : 'day');
    if (filters) {
      if (st.st.server) { p += '&server=' + encodeURIComponent(st.st.server); }
      if (st.st.tool) { p += '&tool=' + encodeURIComponent(st.st.tool); }
      if (st.st.client) { p += '&client=' + encodeURIComponent(st.st.client); }
    }
    return p;
  }

  function uniq(rows, field) {
    var seen = {}, out = [];
    (rows || []).forEach(function (r) {
      var v = r[field];
      if (v && !seen[v]) { seen[v] = 1; out.push(v); }
    });
    return out.sort();
  }

  // Списки фильтров берём из запроса без самих фильтров: иначе выбранное значение
  // исчезает из своего же списка и фильтр залипает на одном варианте.
  // Списки зависят только от периода и набора, поэтому запрос за ними — не на каждый чин.
  function loadStats() {
    var seq = ++st.st.seq;
    var key = st.st.period + '|' + (st.st.set || 'all');
    var base;
    if (key === st.st.facetKey && st.st.facets) {
      base = Promise.resolve(null);
    } else {
      st.st.facetKey = key;
      base = get(statsPath(false)).then(function (d) {
        if (seq !== st.st.seq) { return null; }
        st.st.facets = d ? {
          servers: uniq(d.servers, 'server'),
          tools: uniq(d.tools, 'tool'),
          clients: uniq(d.clients, 'client')
        } : { servers: [], tools: [], clients: [] };
        fillFilters();
        return get(statsPath(true));
      });
    }
    return base.then(function (data) {
      if (seq !== st.st.seq) { return null; }
      return data || get(statsPath(true));
    }).then(function (data) {
      if (seq !== st.st.seq) { return; }
      st.st.data = data || null;
      renderStats(data);
    }).catch(function (e) {
      if (seq === st.st.seq) { statsFail(e); }
    });
  }

  function statsFail(e) {
    st.st.data = null;
    renderStats(null);
    $('stats-empty-why').textContent = 'запрос срезов не удался: ' + e.message;
  }

  function setPeriod(p, fromUrl) {
    if (PERIODS.indexOf(p) < 0) { p = 'today'; }
    var same = p === st.st.period;
    st.st.period = p;
    [].forEach.call(document.querySelectorAll('#s-period .segbtn'), function (b) {
      var on = b.dataset.period === p;
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      b.tabIndex = on ? 0 : -1;
    });
    if (!fromUrl) { history.replaceState(null, '', '#/stats/' + p); }
    if (!same || !st.st.data) { loadStats(); }
  }

  // Пересобираем <option> только когда набор значений реально изменился: иначе
  // список пересыпается на каждом обновлении и сбрасывает выбор.
  function fillSelect(node, values, current, allLabel, label) {
    var key = (values || []).join('\u0001');
    if (node.dataset.key !== key) {
      node.dataset.key = key;
      var frag = document.createDocumentFragment();
      var all = el('option', '', allLabel);
      all.value = '';
      frag.appendChild(all);
      (values || []).forEach(function (v) {
        var o = el('option', '', label ? label(v) : v);
        o.value = v;
        frag.appendChild(o);
      });
      node.replaceChildren(frag);
    }
    node.value = current || '';
  }

  function fillFilters() {
    // вызывается и до первой загрузки срезов — тогда facets ещё нет
    var f = st.st.facets || { servers: [], tools: [], clients: [] };
    fillSelect($('s-set'), st.sets.map(function (s) { return s.id; }), st.st.set, 'все наборы', setName);
    fillSelect($('s-server'), f.servers, st.st.server, 'все');
    fillSelect($('s-tool'), f.tools, st.st.tool, 'все');
    fillSelect($('s-client'), f.clients, st.st.client, 'все');
  }

  function setName(id) {
    for (var i = 0; i < st.sets.length; i++) {
      if (st.sets[i].id === id) { return st.sets[i].name || id; }
    }
    return id;
  }

  function renderStats(d) {
    var t = (d && d.totals) || { calls: 0 };
    var empty = !t.calls;
    show($('stats-empty'), empty);
    show($('s-kpis'), !empty);
    show($('s-charts'), !empty);

    var unreach = (d && d.unreachable) || [];
    show($('stats-unreach'), unreach.length > 0);
    $('stats-unreach').textContent = unreach.length ? 'нет ответа: ' + unreach.join(', ') : '';

    if (empty) {
      $('stats-empty-why').textContent = (st.st.server || st.st.tool || st.st.client)
        ? 'фильтры сузили выборку до нуля — сбросьте их или возьмите период пошире'
        : 'за период «' + PERIOD_NAMES[st.st.period] + '» вызовов нет';
      return;
    }

    $('s-calls').textContent = fmtN(t.calls);
    $('s-fail').textContent = pct(t.failures, t.calls);
    $('s-p50').textContent = ms(t.p50ms);
    $('s-p95').textContent = ms(t.p95ms);
    show($('s-p50-a'), !!d.approximate);
    show($('s-p95-a'), !!d.approximate);

    renderSeries(d.series || []);
    $('s-tools-count').textContent = fmtN((d.tools || []).length);
    renderBreakdown($('tools-body'), d.tools, 'tool', $('tools-empty'), 8, true);
    renderBreakdown($('byserver-body'), d.servers, 'server', $('byserver-empty'), 8);
    renderBreakdown($('byclient-body'), d.clients, 'client', $('byclient-empty'), 6);
  }

  // group — сортировка готовых строк бэкенда по вызовам, без собственной агрегации.
  function renderBreakdown(tbody, rows, field, emptyNode, limit, withServer) {
    var list = (rows || []).slice().sort(function (a, b) { return (b.calls || 0) - (a.calls || 0); });
    show(emptyNode, list.length === 0);
    var frag = document.createDocumentFragment();
    list.slice(0, limit || list.length).forEach(function (g) {
      var tr = el('tr');
      var name = tr.appendChild(el('td'));
      name.appendChild(el('span', 'od-truncate', g[field] || '—'));
      name.title = withServer && g.server ? g.server + ' / ' + g.tool : (g[field] || '');
      tr.appendChild(el('td', 'num n', fmtN(g.calls)));
      var f = tr.appendChild(el('td', 'num n', pct(g.failures, g.calls)));
      if (g.failures) { f.classList.add('bad'); }
      tr.appendChild(el('td', 'num n col-x', ms(g.p50ms)));
      tr.appendChild(el('td', 'num n col-x', ms(g.p95ms)));
      frag.appendChild(tr);
    });
    if (limit && list.length > limit) {
      var rest = frag.appendChild(el('tr', 'more'));
      var td = rest.appendChild(el('td', '', 'ещё ' + fmtN(list.length - limit) + ' — сузьте фильтр'));
      td.colSpan = 5;
    }
    tbody.replaceChildren(frag);
  }

  // series — вызовы по корзинам периода (час для «сегодня», день для остальных).
  function renderSeries(rows) {
    var box = $('activity-bars');
    var hour = st.st.period === 'today';
    show($('activity-empty'), rows.length === 0);
    $('s-activity-title').textContent = hour ? 'Вызовы по часам' : 'Вызовы по дням';
    if (!rows.length) {
      box.replaceChildren();
      $('s-span').textContent = '—';
      $('s-from').textContent = '—';
      $('s-to').textContent = '—';
      return;
    }

    var max = 1, calls = 0;
    rows.forEach(function (r) {
      calls += r.calls || 0;
      max = Math.max(max, r.calls || 0);
    });

    var frag = document.createDocumentFragment();
    rows.forEach(function (r) {
      var n = r.calls || 0, err = Math.min(r.failures || 0, n);
      var col = el('div', 'bcol');
      if (err) {
        var e = el('div', 'bseg err');
        e.style.height = (err / max * 100) + '%';
        col.appendChild(e);
      }
      if (n - err > 0) {
        var o = el('div', 'bseg');
        o.style.height = ((n - err) / max * 100) + '%';
        col.appendChild(o);
      }
      var when = new Date(r.t);
      col.title = (hour ? hm(when.getTime()) : day(r.t)) + ' · ' + n + ' вызовов' +
        (err ? ', ошибок ' + err : '');
      frag.appendChild(col);
    });
    box.replaceChildren(frag);

    $('s-span').textContent = fmtN(calls) + ' вызовов · ' + rows.length + (hour ? ' ч' : ' дн');
    var first = new Date(rows[0].t), last = new Date(rows[rows.length - 1].t);
    $('s-from').textContent = hour ? hm(first.getTime()) : day(rows[0].t);
    $('s-to').textContent = hour ? hm(last.getTime()) : day(rows[rows.length - 1].t);
  }

  /* ---------- вкладки основной области ---------- */

  function setTab(name) {
    st.tab = name;
    ['overview', 'calls', 'stats'].forEach(function (t) {
      var btn = $('tabbtn-' + t);
      var on = t === name;
      btn.setAttribute('aria-selected', on ? 'true' : 'false');
      btn.tabIndex = on ? 0 : -1;
      show($('tab-' + t), on);
    });
    // адрес повторяет то, что открыто: у статистики в нём ещё и период
    var hash = name === 'stats' ? '#/stats/' + st.st.period : (st.sel ? '#/set/' + st.sel : '');
    if (hash && location.hash !== hash) { history.replaceState(null, '', hash); }
  }

  /* ---------- панель деталей ---------- */

  function setSideTab(name) {
    st.sideTab = name;
    ['servers', 'env', 'log'].forEach(function (p) {
      var btn = document.querySelector('.segbtn[data-side="' + p + '"]');
      var on = p === name;
      btn.setAttribute('aria-selected', on ? 'true' : 'false');
      btn.tabIndex = on ? 0 : -1;
      show(document.querySelector('.pane[data-pane="' + p + '"]'), on);
    });
  }

  function openSide() {
    document.body.classList.add('side-open');
    $('side-open').setAttribute('aria-expanded', 'true');
    show($('scrim'), window.innerWidth < 1280);
  }

  function closeSide() {
    document.body.classList.remove('side-open');
    $('side-open').setAttribute('aria-expanded', 'false');
    show($('scrim'), false);
  }

  function applyRail() {
    var w = window.innerWidth;
    // ниже 1200 колонке деталей места нет — она в дровере, рельс отдаём иконкам;
    // но выбор пользователя шевроном сильнее ширины окна, иначе кнопка мертва в 960–1199
    var auto = w >= 960 && w < 1200;
    var compact = st.railPref ? st.railPref === 'compact' : auto;
    $('rail').classList.toggle('compact', compact);
    var btn = $('rail-toggle');
    btn.hidden = w < 960;
    btn.setAttribute('aria-pressed', String(compact));
    btn.setAttribute('aria-label', compact ? 'Развернуть список наборов' : 'Свернуть список наборов');
    if (w >= 1200) { closeSide(); }
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
        tr.appendChild(el('td', 'hide-sm'));
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

  $('sets-body').addEventListener('click', function (e) {
    var tr = e.target.closest('tr');
    if (tr && tr.dataset.id && tr.dataset.id !== st.sel) { select(tr.dataset.id); }
  });

  $('tabs').addEventListener('click', function (e) {
    var btn = e.target.closest('.tab');
    if (btn) { setTab(btn.dataset.tab); }
  });
  $('tabs').addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') { return; }
    var order = ['overview', 'calls', 'stats'];
    var i = order.indexOf(st.tab) + (e.key === 'ArrowRight' ? 1 : -1);
    if (i < 0) { i = order.length - 1; }
    if (i >= order.length) { i = 0; }
    setTab(order[i]);
    $('tabbtn-' + order[i]).focus();
  });

  document.querySelector('.seg').addEventListener('click', function (e) {
    var btn = e.target.closest('.segbtn');
    if (btn) { setSideTab(btn.dataset.side); }
  });

  $('side-open').addEventListener('click', openSide);
  $('side-close').addEventListener('click', closeSide);
  $('scrim').addEventListener('click', closeSide);
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && document.body.classList.contains('side-open')) { closeSide(); }
  });

  $('rail-toggle').addEventListener('click', function () {
    st.railPref = $('rail').classList.contains('compact') ? 'full' : 'compact';
    try { localStorage.setItem('mcprig.rail', st.railPref); } catch (err) { /* приватный режим */ }
    applyRail();
  });

  $('q').addEventListener('input', function () { renderCalls(st.calls); });
  $('only-err').addEventListener('change', function () { renderCalls(st.calls); });

  ['s-set', 's-server', 's-tool', 's-client'].forEach(function (id) {
    $(id).addEventListener('change', function () {
      st.st.set = $('s-set').value;
      st.st.server = $('s-server').value;
      st.st.tool = $('s-tool').value;
      st.st.client = $('s-client').value;
      loadStats();
    });
  });
  $('s-reset').addEventListener('click', function () {
    st.st.set = '';
    st.st.server = '';
    st.st.tool = '';
    st.st.client = '';
    fillFilters();
    loadStats();
  });
  [].forEach.call(document.querySelectorAll('#s-period .segbtn'), function (btn) {
    btn.addEventListener('click', function () { setPeriod(btn.dataset.period, false); });
  });

  document.addEventListener('keydown', function (e) {
    var tag = document.activeElement && document.activeElement.tagName;
    if (e.key !== '/' || tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') { return; }
    e.preventDefault();
    if (st.tab !== 'calls') { setTab('calls'); }
    $('q').focus();
  });

  window.addEventListener('hashchange', function () {
    var m = /^#\/set\/([^/]+)$/.exec(location.hash);
    if (m && m[1] !== st.sel) { select(decodeURIComponent(m[1])); return; }
    var s = /^#\/stats\/([a-z0-9]+)$/.exec(location.hash);
    if (s) { setTab('stats'); setPeriod(s[1], true); }
  });

  window.addEventListener('resize', applyRail);

  $('dim').addEventListener('click', function () {
    var on = this.getAttribute('aria-pressed') === 'true';
    this.setAttribute('aria-pressed', String(!on));
    document.documentElement.classList.toggle('dim', !on);
  });

  // схему выбирает select в шапке; сам атрибут ставит theme.js до первой отрисовки
  var themeSel = $('theme');
  themeSel.value = document.documentElement.getAttribute('data-theme');
  themeSel.addEventListener('change', function () {
    document.documentElement.setAttribute('data-theme', themeSel.value);
    try { localStorage.setItem('mcprig.theme', themeSel.value); } catch (e) { /* приватный режим */ }
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

  $('tail-all').addEventListener('click', function () { setTab('calls'); });
  $('log-refresh').addEventListener('click', function () { loadLog(); });
  // лог — единственная панель, которой тесно в 330 px: даём развернуть вручную
  $('log-wide').addEventListener('click', function () {
    var on = document.body.classList.toggle('side-wide');
    this.setAttribute('aria-pressed', on ? 'true' : 'false');
  });
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

  function every(ms, fn) { setInterval(function () { if (!document.hidden) { fn(); } }, ms); }

  try { var rp = localStorage.getItem('mcprig.rail'); st.railPref = rp === 'compact' || rp === 'full' ? rp : ''; } catch (e) { /* приватный режим */ }
  // ссылка на срез статистики открывает сразу её
  var statLink = /^#\/stats\/([a-z0-9]+)$/.exec(location.hash);
  if (statLink) { st.st.period = PERIODS.indexOf(statLink[1]) < 0 ? 'today' : statLink[1]; st.tab = 'stats'; }
  applyRail();
  setTab(st.tab);
  setPeriod(st.st.period, true);
  setSideTab(st.sideTab);
  loadEnv();
  loadState().then(function () {
    every(5000, loadState);
    every(5000, function () { if (st.sel) { loadHealth(); loadCalls(); } });
    every(10000, function () {
      var set = current();
      if (set && set.runtime && set.runtime.running) { loadLog(); }
    });
    every(15000, loadStats);
    every(30000, loadEnv);
  });
})();
