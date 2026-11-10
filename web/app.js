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

  /* ---------- язык ----------
     Все пользовательские строки — в DICT: английский по умолчанию, русский
     переключателем в шапке. Выбор живёт в localStorage, <html lang> следует за ним. */

  var DICT = {
    en: {
      'title.panel': 'mcprig — panel',
      'title.set': 'mcprig — set {name}',
      'top.details': 'Details',
      'top.lang': 'Language',
      'top.theme': 'Color scheme',
      'top.dim': 'Dim the panel',
      'top.who': 'Vladimir',
      'theme.light': 'Light',
      'rail.title': 'Sets',
      'rail.collapse': 'Collapse the set list',
      'rail.expand': 'Expand the set list',
      'rail.empty': 'no sets',
      'rail.add': 'set',
      'rail.name': 'name',
      'rail.port': 'port',
      'rail.create': 'create',
      'rail.item': '{name} · port {port} · {state}',
      'common.cancel': 'cancel',
      'common.hide': 'hide',
      'common.save': 'save',
      'common.close': 'close',
      'common.done': 'done',
      'empty.title': 'No sets',
      'empty.h': 'Build your first endpoint',
      'empty.p': 'A set gets a port from 9100–9199, a key, and one MCP address for all of its servers.',
      'empty.s1': 'Add a server: a stdio command or a streamable-http URL.',
      'empty.s2': 'Create a set and assign servers to it.',
      'empty.s3': 'Copy the endpoint and the key into your client.',
      'empty.add': 'Create a set',
      'head.copy': 'copy',
      'head.copied': 'copied',
      'head.copyFail': 'failed',
      'head.setName': 'Set {name}',
      'head.key': 'key',
      'head.keyTip': 'only the prefix is shown',
      'head.keyOpen': 'set is open',
      'head.noKey': 'no key',
      'head.schemaTip': "how much context the set's tool list costs",
      'head.keys': 'Keys',
      'head.start': 'Start',
      'head.restart': 'Restart',
      'head.stopped': 'set stopped',
      'head.exited': 'process exited (code {code})',
      'head.exitCode': 'exit code {code}',
      'head.restarts': 'restarts: {n}',
      'tabs.label': 'Section',
      'tabs.overview': 'Overview',
      'tabs.calls': 'Calls',
      'tabs.stats': 'Statistics',
      'kpi.calls': 'calls',
      'kpi.errors': 'errors',
      'setSrv.title': 'Servers of this set',
      'setSrv.hint': 'state comes from the running set',
      'setSrv.caption': 'Servers of the selected set: type, state, isolation and tags',
      'setSrv.thServer': 'Server',
      'setSrv.thType': 'Type',
      'setSrv.thState': 'State',
      'setSrv.thIso': 'Isolation',
      'setSrv.thTags': 'Tags',
      'setSrv.empty': 'the set has no servers',
      'sets.title': 'Sets',
      'sets.hint': 'one entry point per set · ports come from the manager',
      'sets.caption': 'Sets with their ports, state and servers',
      'sets.thSet': 'Set',
      'sets.thPort': 'Port',
      'sets.thState': 'State',
      'sets.thServers': 'Servers',
      'sets.thUptime': 'Uptime',
      'sets.thCalls': 'Calls',
      'sets.thErrors': 'Errors',
      'sets.callsTip': 'for the period chosen on the statistics tab',
      'tail.title': 'Recent calls',
      'tail.all': 'all calls',
      'tail.caption': 'The last eight calls of the selected set',
      'tail.thTime': 'Time',
      'tail.thServer': 'Server',
      'tail.thTool': 'Tool',
      'tail.thMs': 'ms',
      'tail.thStatus': 'Status',
      'tail.empty': 'no calls yet — the set has not received requests',
      'tail.span': 'latest {n}',
      'calls.search': 'Search calls',
      'calls.ph': 'search calls',
      'calls.onlyErr': 'errors only',
      'calls.caption': 'Recent calls of the selected set',
      'calls.thClient': 'Client',
      'calls.unavailable': 'call metrics are unavailable: the /api/calls route is not up yet',
      'calls.of': '{n} of {m}',
      'calls.noMatch': 'nothing found — loosen the filter',
      'calls.bad': 'error',
      'calls.ok': 'ok',
      'stats.period': 'Period',
      'stats.today': 'Today',
      'stats.7d': '7 days',
      'stats.30d': '30 days',
      'stats.archive': 'Archive',
      'stats.set': 'set',
      'stats.allSets': 'all sets',
      'stats.server': 'server',
      'stats.tool': 'tool',
      'stats.client': 'client',
      'stats.all': 'all',
      'stats.reset': 'reset',
      'stats.approxTip': 'from the histogram',
      'stats.byHour': 'Calls by hour',
      'stats.byDay': 'Calls by days',
      'stats.byMonth': 'Calls by months',
      'stats.bars': 'Calls per bucket of the period',
      'stats.noCalls': 'no calls in this period',
      'stats.topTools': 'Top tools',
      'stats.byServer': 'By server',
      'stats.byClient': 'By client',
      'stats.noData': 'no data',
      'stats.emptyStrong': 'Nothing to show',
      'stats.emptyFilters': 'the filters narrowed the sample to zero — reset them or widen the period',
      'stats.emptyPeriod': 'no calls in the period “{p}”',
      'stats.unreach': 'no response: {list}',
      'stats.fail': 'the stats request failed: {msg}',
      'stats.more': '{n} more — narrow the filter',
      'stats.colTip': '{label} · {n} calls',
      'stats.colTipErr': '{label} · {n} calls, {err} errors',
      'side.label': 'Set details',
      'side.servers': 'Servers',
      'side.env': 'Variables',
      'side.log': 'Log',
      'side.close': 'Close the panel',
      'side.registry': 'host-wide registry',
      'side.addServer': 'add server',
      'side.registryEmpty': 'registry is empty',
      'side.server': 'server',
      'form.description': 'description',
      'form.transport': 'transport',
      'form.stdio': 'stdio — local process',
      'form.http': 'http — remote endpoint',
      'form.command': 'command · one argument per line',
      'form.cwd': 'working directory',
      'form.headers': 'headers · K: V',
      'form.env': 'env · KEY=value',
      'form.envAllow': 'envAllow · one per line',
      'form.tags': 'tags · comma separated',
      'form.enabled': 'enabled',
      'form.sandbox': 'sandbox',
      'form.sandboxHint': 'mode bwrap | none · onUnavailable deny | warn | allow · network none | allow · empty means the default',
      'form.unset': 'not set',
      'form.ro': 'ro · one per line',
      'form.rw': 'rw · one per line',
      'form.delete': 'delete server',
      'form.editing': 'server · {name}',
      'form.new': 'new server',
      'form.create': 'create',
      'form.deleteConfirm': 'Delete server {name} from the host registry? It will disappear from the sets that use it.',
      'env.empty': 'no variables',
      'env.note': 'values are supplied by the set process — the panel neither stores nor shows them',
      'env.set': 'set',
      'env.unset': 'not set',
      'log.tail': 'log tail of the set',
      'log.wide': 'wider',
      'log.wideTip': 'expand the log panel',
      'log.refresh': 'refresh',
      'log.empty': 'log is empty',
      'keys.title': 'Set keys',
      'keys.thKey': 'Key',
      'keys.thCreated': 'Created',
      'keys.thNote': 'Note',
      'keys.thAction': 'Action',
      'keys.note': 'Only whoever issued a token knows it: the panel shows the first 8 characters. Revoking a key takes effect without restarting the set.',
      'keys.issue': 'issue a key',
      'keys.revoke': 'revoke',
      'keys.revoked': 'revoked {date}',
      'keys.active': 'active',
      'status.running': 'running',
      'status.failed': 'failed',
      'status.stopped': 'stopped',
      'iso.okTip': 'bwrap sandbox applied',
      'iso.none': 'no isolation',
      'iso.rejected': 'config rejected',
      'iso.rejectedTip': "config rejected: the server won't start",
      'unit.b': 'B',
      'unit.kb': 'KB',
      'unit.tokens': 'tok',
      'unit.tools': 'tools',
      'unit.h': 'h',
      'unit.m': 'm',
      'unit.hour': 'h',
      'unit.day': 'd',
      'unit.month': 'mo',
      'unit.ms': '{n} ms',
      'schema.line': '{bytes} · {tokens} {tok} · {tools} {tool}',
      'srv.edit': 'edit',
      'srv.probe': 'probe',
      'srv.probing': 'probing…',
      'srv.notProbed': 'not probed',
      'srv.notInSet': 'not in this set',
      'srv.setOff': 'set is not running',
      'srv.unchecked': 'not checked',
      'srv.ok': 'health ok',
      'srv.noResponse': 'no response',
      'srv.ok2': 'ok',
      'srv.probeOk': 'probe ok · {ms} ms · {n} tools',
      'srv.probeFail': 'probe failed',
      'srv.notFound': 'server {name} is not in the registry',
      'srv.noServers': 'no servers',
      'top.updated': 'updated {time}',
      'top.runningW': 'running',
      'err.offline': 'no connection to the daemon',
      'err.http': 'HTTP {status} · {path}',
      'err.network': 'network error',
      'add.nameRequired': 'a name is required'
    },
    ru: {
      'title.panel': 'mcprig — панель',
      'title.set': 'mcprig — набор {name}',
      'top.details': 'Детали',
      'top.lang': 'Язык',
      'top.theme': 'Цветовая схема',
      'top.dim': 'Понизить яркость панели',
      'top.who': 'Владимир',
      'theme.light': 'Светлая',
      'rail.title': 'Наборы',
      'rail.collapse': 'Свернуть список наборов',
      'rail.expand': 'Развернуть список наборов',
      'rail.empty': 'наборов нет',
      'rail.add': 'набор',
      'rail.name': 'имя',
      'rail.port': 'порт',
      'rail.create': 'создать',
      'rail.item': '{name} · порт {port} · {state}',
      'common.cancel': 'отмена',
      'common.hide': 'скрыть',
      'common.save': 'сохранить',
      'common.close': 'закрыть',
      'common.done': 'готово',
      'empty.title': 'Нет наборов',
      'empty.h': 'Соберите первый endpoint',
      'empty.p': 'Набор получает порт из 9100–9199, ключ и один MCP-адрес на все свои серверы.',
      'empty.s1': 'Добавьте сервер: stdio-команда или streamable-http URL.',
      'empty.s2': 'Создайте набор и назначьте ему серверы.',
      'empty.s3': 'Скопируйте endpoint и ключ в клиент.',
      'empty.add': 'Создать набор',
      'head.copy': 'копировать',
      'head.copied': 'скопировано',
      'head.copyFail': 'не удалось',
      'head.setName': 'Набор {name}',
      'head.key': 'ключ',
      'head.keyTip': 'показывается только префикс',
      'head.keyOpen': 'набор открыт',
      'head.noKey': 'ключа нет',
      'head.schemaTip': 'сколько контекста стоит перечень инструментов набора',
      'head.keys': 'Ключи',
      'head.start': 'Запустить',
      'head.restart': 'Перезапустить',
      'head.stopped': 'набор остановлен',
      'head.exited': 'процесс завершился (code {code})',
      'head.exitCode': 'код выхода {code}',
      'head.restarts': 'автоперезапусков: {n}',
      'tabs.label': 'Раздел',
      'tabs.overview': 'Обзор',
      'tabs.calls': 'Вызовы',
      'tabs.stats': 'Статистика',
      'kpi.calls': 'вызовов',
      'kpi.errors': 'ошибок',
      'setSrv.title': 'Серверы набора',
      'setSrv.hint': 'состояние — из запущенного набора',
      'setSrv.caption': 'Серверы выбранного набора: тип, состояние, изоляция и теги',
      'setSrv.thServer': 'Сервер',
      'setSrv.thType': 'Тип',
      'setSrv.thState': 'Состояние',
      'setSrv.thIso': 'Изоляция',
      'setSrv.thTags': 'Теги',
      'setSrv.empty': 'в наборе нет серверов',
      'sets.title': 'Наборы',
      'sets.hint': 'одна точка входа на набор · порты выдаёт менеджер',
      'sets.caption': 'Наборы, их порты, состояние и серверы',
      'sets.thSet': 'Набор',
      'sets.thPort': 'Порт',
      'sets.thState': 'Состояние',
      'sets.thServers': 'Серверов',
      'sets.thUptime': 'Аптайм',
      'sets.thCalls': 'Вызовов',
      'sets.thErrors': 'Ошибок',
      'sets.callsTip': 'за период, выбранный на вкладке статистики',
      'tail.title': 'Последние вызовы',
      'tail.all': 'все вызовы',
      'tail.caption': 'Восемь последних вызовов выбранного набора',
      'tail.thTime': 'Время',
      'tail.thServer': 'Сервер',
      'tail.thTool': 'Инструмент',
      'tail.thMs': 'мс',
      'tail.thStatus': 'Статус',
      'tail.empty': 'вызовов пока нет — набор ещё не получал запросов',
      'tail.span': 'последние {n}',
      'calls.search': 'Поиск по вызовам',
      'calls.ph': 'поиск по вызовам',
      'calls.onlyErr': 'только ошибки',
      'calls.caption': 'Последние вызовы выбранного набора',
      'calls.thClient': 'Клиент',
      'calls.unavailable': 'метрики вызовов недоступны: роут /api/calls ещё не поднят',
      'calls.of': '{n} из {m}',
      'calls.noMatch': 'ничего не нашлось — ослабьте фильтр',
      'calls.bad': 'ошибка',
      'calls.ok': 'ok',
      'stats.period': 'Период',
      'stats.today': 'Сегодня',
      'stats.7d': '7 дней',
      'stats.30d': '30 дней',
      'stats.archive': 'Архив',
      'stats.set': 'набор',
      'stats.allSets': 'все наборы',
      'stats.server': 'сервер',
      'stats.tool': 'инструмент',
      'stats.client': 'клиент',
      'stats.all': 'все',
      'stats.reset': 'сбросить',
      'stats.approxTip': 'по гистограмме',
      'stats.byHour': 'Вызовы по часам',
      'stats.byDay': 'Вызовы по дням',
      'stats.byMonth': 'Вызовы по месяцам',
      'stats.bars': 'Вызовы по корзинам периода',
      'stats.noCalls': 'в этом периоде вызовов нет',
      'stats.topTools': 'Топ инструментов',
      'stats.byServer': 'По серверам',
      'stats.byClient': 'По клиентам',
      'stats.noData': 'данных нет',
      'stats.emptyStrong': 'Статистике нечего показать',
      'stats.emptyFilters': 'фильтры сузили выборку до нуля — сбросьте их или возьмите период пошире',
      'stats.emptyPeriod': 'за период «{p}» вызовов нет',
      'stats.unreach': 'нет ответа: {list}',
      'stats.fail': 'запрос срезов не удался: {msg}',
      'stats.more': 'ещё {n} — сузьте фильтр',
      'stats.colTip': '{label} · {n} вызовов',
      'stats.colTipErr': '{label} · {n} вызовов, ошибок {err}',
      'side.label': 'Детали набора',
      'side.servers': 'Серверы',
      'side.env': 'Переменные',
      'side.log': 'Лог',
      'side.close': 'Закрыть панель',
      'side.registry': 'общий реестр хоста',
      'side.addServer': 'добавить сервер',
      'side.registryEmpty': 'реестр пуст',
      'side.server': 'сервер',
      'form.description': 'описание',
      'form.transport': 'транспорт',
      'form.stdio': 'stdio — локальный процесс',
      'form.http': 'http — удалённый endpoint',
      'form.command': 'команда · по строке на аргумент',
      'form.cwd': 'рабочий каталог',
      'form.headers': 'заголовки · K: V',
      'form.env': 'env · KEY=значение',
      'form.envAllow': 'envAllow · по строке',
      'form.tags': 'теги · через запятую',
      'form.enabled': 'включён',
      'form.sandbox': 'песочница',
      'form.sandboxHint': 'mode bwrap | none · onUnavailable deny | warn | allow · network none | allow · пусто — умолчание',
      'form.unset': 'не задан',
      'form.ro': 'ro · по строке',
      'form.rw': 'rw · по строке',
      'form.delete': 'удалить сервер',
      'form.editing': 'сервер · {name}',
      'form.new': 'новый сервер',
      'form.create': 'создать',
      'form.deleteConfirm': 'Удалить сервер {name} из общего реестра? Он исчезнет из наборов, которые его используют.',
      'env.empty': 'переменных нет',
      'env.note': 'значения подставляет процесс набора — панель их не хранит и не показывает',
      'env.set': 'задана',
      'env.unset': 'не задана',
      'log.tail': 'хвост лога набора',
      'log.wide': 'шире',
      'log.wideTip': 'развернуть панель лога',
      'log.refresh': 'обновить',
      'log.empty': 'лог пуст',
      'keys.title': 'Ключи набора',
      'keys.thKey': 'Ключ',
      'keys.thCreated': 'Создан',
      'keys.thNote': 'Примечание',
      'keys.thAction': 'Действие',
      'keys.note': 'Токен знает только тот, кто его выпустил: панель показывает лишь первые 8 символов. Отзыв ключа действует без перезапуска набора.',
      'keys.issue': 'выпустить ключ',
      'keys.revoke': 'отозвать',
      'keys.revoked': 'отозван {date}',
      'keys.active': 'действует',
      'status.running': 'работает',
      'status.failed': 'упал',
      'status.stopped': 'остановлен',
      'iso.okTip': 'песочница bwrap применена',
      'iso.none': 'без изоляции',
      'iso.rejected': 'конфиг отвергнут',
      'iso.rejectedTip': 'конфиг отвергнут: сервер не запустится',
      'unit.b': 'Б',
      'unit.kb': 'КБ',
      'unit.tokens': 'ток',
      'unit.tools': 'инстр.',
      'unit.h': 'ч',
      'unit.m': 'м',
      'unit.hour': 'ч',
      'unit.day': 'дн',
      'unit.month': 'мес',
      'unit.ms': '{n} мс',
      'schema.line': '{bytes} · {tokens} {tok} · {tools} {tool}',
      'srv.edit': 'изменить',
      'srv.probe': 'проверить',
      'srv.probing': 'проверка…',
      'srv.notProbed': 'не проверялся',
      'srv.notInSet': 'не в наборе',
      'srv.setOff': 'набор не запущен',
      'srv.unchecked': 'не проверено',
      'srv.ok': 'health ok',
      'srv.noResponse': 'нет ответа',
      'srv.ok2': 'ok',
      'srv.probeOk': 'проверка ok · {ms} мс · инструментов {n}',
      'srv.probeFail': 'проверка не удалась',
      'srv.notFound': 'сервер {name} не найден в реестре',
      'srv.noServers': 'серверов нет',
      'top.updated': 'обновлено {time}',
      'top.runningW': 'запущено',
      'err.offline': 'нет связи с демоном',
      'err.http': 'HTTP {status} · {path}',
      'err.network': 'ошибка сети',
      'add.nameRequired': 'имя обязательно'
    }
  };

  var LANG_KEY = 'mcprig.lang';
  var lang = 'en';
  try {
    var savedLang = localStorage.getItem(LANG_KEY);
    if (savedLang === 'ru' || savedLang === 'en') { lang = savedLang; }
  } catch (e) { /* приватный режим */ }

  // склонения: у ru три формы, у en две — ради «1 набор» против «5 наборов»
  var PLURAL = {
    set: { en: ['set', 'sets'], ru: ['набор', 'набора', 'наборов'] },
    server: { en: ['server', 'servers'], ru: ['сервер', 'сервера', 'серверов'] },
    call: { en: ['call', 'calls'], ru: ['вызов', 'вызова', 'вызовов'] }
  };

  function word(n, kind) {
    var f = PLURAL[kind][lang];
    if (lang !== 'ru') { return n === 1 ? f[0] : f[1]; }
    if (n % 10 === 1 && n % 100 !== 11) { return f[0]; }
    if (n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14)) { return f[1]; }
    return f[2];
  }

  function t(k, v) {
    var s = (DICT[lang] && DICT[lang][k]) || DICT.en[k] || k;
    if (!v) { return s; }
    return s.replace(/\{(\w+)\}/g, function (m, name) {
      return v[name] == null ? m : String(v[name]);
    });
  }

  // числа и даты — через Intl: разделители и порядок частей следуют языку
  var LOCALE = { en: 'en-US', ru: 'ru-RU' };
  var FMT = {};

  function fmtCached(key, opts) {
    var id = lang + '|' + key;
    return FMT[id] || (FMT[id] = new Intl.DateTimeFormat(LOCALE[lang], opts));
  }

  function numFmt(opts) {
    var id = lang + '|n|' + JSON.stringify(opts || {});
    return FMT[id] || (FMT[id] = new Intl.NumberFormat(LOCALE[lang], opts || {}));
  }

  function applyStatic() {
    document.documentElement.setAttribute('lang', lang);
    [].forEach.call(document.querySelectorAll('[data-i18n]'), function (n) {
      n.textContent = t(n.dataset.i18n);
    });
    [].forEach.call(document.querySelectorAll('[data-i18n-aria]'), function (n) {
      n.setAttribute('aria-label', t(n.dataset.i18nAria));
    });
    [].forEach.call(document.querySelectorAll('[data-i18n-title]'), function (n) {
      n.setAttribute('title', t(n.dataset.i18nTitle));
    });
    [].forEach.call(document.querySelectorAll('[data-i18n-ph]'), function (n) {
      n.setAttribute('placeholder', t(n.dataset.i18nPh));
    });
  }

  // набор из адреса (#/set/demo) выбирается сразу, данные подтягивает первый /api/state
  var linkId = (/^#\/set\/([^/]+)$/.exec(location.hash) || [])[1];
  var st = {
    sets: [], servers: [], sel: linkId ? decodeURIComponent(linkId) : null,
    health: null, calls: null, env: [], logKey: '', errTimer: 0, busy: false, boot: !!linkId,
    tab: 'overview', sideTab: 'servers', railPref: '',
    // mock — состояние демо-реестра, правки в нём живут до перезагрузки;
    // srvEditing/srvReturn — какая строка открыта в форме и куда вернётся фокус
    mock: null, srvEditing: '', srvReturn: null,
    st: { period: 'today', set: '', server: '', tool: '', client: '', seq: 0, data: null, facets: null, facetKey: '' }
  };

  /* ---------- мелочи ---------- */

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) { n.className = cls; }
    if (text != null) { n.textContent = String(text); }
    return n;
  }

  // Щит одинаков для всех состояний: цвет и подпись несут смысл, иконка — узнавание
  function shield() {
    var ns = 'http://www.w3.org/2000/svg';
    var s = document.createElementNS(ns, 'svg');
    s.setAttribute('viewBox', '0 0 24 24');
    s.setAttribute('fill', 'none');
    s.setAttribute('stroke', 'currentColor');
    s.setAttribute('stroke-width', '1.8');
    s.setAttribute('stroke-linejoin', 'round');
    s.setAttribute('aria-hidden', 'true');
    var p = document.createElementNS(ns, 'path');
    p.setAttribute('d', 'M12 3l7 3.5V12c0 4.2-2.9 7.4-7 9-4.1-1.6-7-4.8-7-9V6.5z');
    s.appendChild(p);
    return s;
  }

  // isolation из list_servers/health: bwrap — норма, «без изоляции: причина» —
  // предупреждение, «нет: конфиг отвергнут» — ошибка. Поля нет — маркера нет.
  function isoOf(v) {
    if (!v) { return null; }
    if (v === 'bwrap') { return { cls: 'iso-ok', label: 'bwrap', title: t('iso.okTip') }; }
    // префиксы приходят из бэкенда по-русски: это контракт поля isolation, а не текст панели
    if (v.indexOf('без изоляции') === 0) { return { cls: 'iso-warn', label: t('iso.none'), title: v }; }
    if (v.indexOf('нет:') === 0) { return { cls: 'iso-err', label: t('iso.rejected'), title: t('iso.rejectedTip') }; }
    return { cls: 'iso-warn', label: v, title: v };
  }

  function fmtN(v) { return v == null || v === '' ? '—' : numFmt().format(Number(v)); }

  function fmtBytes(v) {
    if (v == null) { return '—'; }
    if (v < 10240) { return fmtN(v) + ' ' + t('unit.b'); }
    return fmtN(Math.round(v / 1024)) + ' ' + t('unit.kb');
  }

  // schemaLine — вес list_commands набора: байты, токены и число инструментов.
  function schemaLine(s) {
    if (!s) { return '—'; }
    return t('schema.line', {
      bytes: fmtBytes(s.schemaBytes), tokens: fmtN(s.schemaTokens),
      tok: t('unit.tokens'), tools: fmtN(s.tools), tool: t('unit.tools')
    });
  }

  function pct(fail, all) {
    if (!all) { return '—'; }
    return numFmt({ minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(fail / all * 100) + ' %';
  }

  function mins(n) { return (n < 10 ? '0' : '') + n; }

  // время и даты — Intl: у ru dd.mm.yyyy и 24-часовой вид, у en свои разделители
  function clock(iso) {
    var d = iso ? new Date(iso) : new Date();
    if (isNaN(d.getTime())) { return '—'; }
    return fmtCached('clock', { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(d);
  }

  function hm(ms) {
    return fmtCached('hm', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(ms));
  }

  function day(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) { return '—'; }
    return fmtCached('day', { day: '2-digit', month: '2-digit', year: 'numeric' }).format(d);
  }

  function mon(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) { return '—'; }
    return fmtCached('mon', { month: '2-digit', year: 'numeric' }).format(d);
  }

  function uptime(set) {
    var from = set.runtime && set.runtime.startedAt;
    if (!set.runtime || !set.runtime.running || !from) { return '—'; }
    var s = Math.max(0, (Date.now() - new Date(from).getTime()) / 1000);
    var h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60);
    return h ? h + ' ' + t('unit.h') + ' ' + mins(m) + ' ' + t('unit.m') : m + ' ' + t('unit.m');
  }

  function setState(set) {
    var r = (set && set.runtime) || {};
    if (r.running) { return { cls: 'ok', text: t('status.running') }; }
    if (r.error || (r.exitCode != null && r.exitCode > 0)) { return { cls: 'err', text: t('status.failed') }; }
    return { cls: 'warn', text: t('status.stopped') };
  }

  // pctl — перцентиль по уже отсортированному массиву чисел.
  function pctl(sorted, p) {
    if (!sorted.length) { return null; }
    var i = (sorted.length - 1) * p;
    var lo = Math.floor(i), hi = Math.ceil(i);
    if (lo === hi) { return sorted[lo]; }
    return Math.round(sorted[lo] + (sorted[hi] - sorted[lo]) * (i - lo));
  }

  function ms(v) { return v == null ? '—' : t('unit.ms', { n: fmtN(v) }); }

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
    return 'state';
  }

  function get(path) {
    if (MOCK) {
      // реестр в демо-режиме живёт в памяти: правка должна быть видна сразу
      if (st.mock && path.indexOf('/api/state') === 0) { return Promise.resolve(st.mock); }
      // срезы считаются из строк, а не читаются готовым файлом: иначе фильтры
      // в демо не на что влиять и цифры врут
      if (path.indexOf('/api/stats') === 0) { return Promise.resolve(mockStats(path)); }
    }
    var url = MOCK ? 'mock/' + mockName(path) + '.json' : path;
    return fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
      .catch(function () { throw new Error(t('err.offline')); })
      .then(function (r) {
        if (r.status === 404) { return null; }
        if (!r.ok) { throw new Error(t('err.http', { status: r.status, path: path })); }
        return r.json();
      })
      .then(function (d) {
        if (MOCK && d && path.indexOf('/api/state') === 0 && !st.mock) { st.mock = d; }
        return d;
      });
  }

  function send(method, path, body) {
    if (MOCK) { return mockSend(method, path, body); }
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

  /* ---------- мок: запись в реестр ----------
     Демо-режим отвечает как реестр: иначе редактор говорит «сохранено» про
     сервер, который настоящий демон отверг бы. Правила повторяют
     internal/registry (имя, транспорт) и internal/config (песочница, теги). */

  var MOCK_SANDBOX_KEYS = ['mode', 'onUnavailable', 'ro', 'rw', 'network', 'memoryMb', 'pids', 'timeoutSec'];

  function mockServerErr(inp, name, exists) {
    if (!/^[a-z0-9][a-z0-9_-]*$/.test(name)) { return 'name: строчные латинские буквы, цифры, - и _'; }
    if (exists) { return 'сервер ' + name + ' уже есть'; }
    var http = inp.transport === 'http';
    if (http && !inp.url) { return 'http требует url'; }
    if (http && inp.command && inp.command.length) { return 'http не может иметь command'; }
    if (!http && !(inp.command && inp.command.length)) { return 'stdio требует command'; }
    if (!http && inp.url) { return 'stdio не может иметь url'; }
    if (http && inp.sandbox) { return 'sandbox допустим только для stdio'; }
    if (inp.sandbox) {
      var extra = Object.keys(inp.sandbox).filter(function (k) { return MOCK_SANDBOX_KEYS.indexOf(k) < 0; });
      if (extra.length) { return 'sandbox: неизвестные поля: ' + extra.join(', '); }
    }
    if ((inp.tags || []).indexOf('untrusted') >= 0 && !inp.sandbox) {
      return 'тег untrusted требует sandbox: сервер без изоляции не стартует';
    }
    return '';
  }

  function mockServer(name, body) {
    var http = body.transport === 'http';
    var srv = { name: name, transport: http ? 'http' : 'stdio', enabled: body.enabled !== false };
    if (body.description) { srv.description = body.description; }
    if (http) { srv.url = body.url || ''; } else { srv.command = body.command || []; }
    if (!http && body.cwd) { srv.cwd = body.cwd; }
    if (body.headers && Object.keys(body.headers).length) { srv.headers = body.headers; }
    if (body.env && Object.keys(body.env).length) { srv.env = body.env; }
    if (body.envAllow && body.envAllow.length) { srv.envAllow = body.envAllow; }
    if (body.tags && body.tags.length) { srv.tags = body.tags; }
    if (body.sandbox && !http) { srv.sandbox = body.sandbox; }
    return srv;
  }

  function mockSend(method, path, body) {
    var m = /^\/api\/servers(?:\/([^/]+))?$/.exec(path);
    if (!m || !st.mock) { return Promise.resolve({ ok: true, ms: 14, tools: [] }); }
    // у POST имя приходит в теле, у PUT/DELETE — в пути
    var name = m[1] ? decodeURIComponent(m[1]) : String((body && body.name) || '');
    var has = function (n) {
      return st.mock.servers.some(function (s) { return s.name === n; });
    };
    var err = '';
    if (method === 'DELETE') {
      if (!has(name)) { err = 'нет сервера ' + name; }
    } else if (method === 'PUT' && !has(name)) {
      err = 'нет сервера ' + name;
    } else {
      err = mockServerErr(body || {}, name, method === 'POST' && has(name));
    }
    if (err) {
      fail(err);
      return Promise.reject(new Error(err));
    }
    if (method === 'DELETE') {
      st.mock.servers = st.mock.servers.filter(function (s) { return s.name !== name; });
      st.mock.sets.forEach(function (s) {
        s.servers = (s.servers || []).filter(function (n) { return n !== name; });
      });
    } else {
      var srv = mockServer(name, body || {});
      if (method === 'POST') {
        st.mock.servers.push(srv);
      } else {
        st.mock.servers = st.mock.servers.map(function (s) { return s.name === name ? srv : s; });
      }
    }
    return Promise.resolve({ ok: true, restarted: [], stopped: [] });
  }

  /* ---------- мок: статистика ----------
     Те же строки, что в raw-*.jsonl, и та же сводка, что в internal/stats:
     окно периода, корзины ряда, разрезы, перцентили (точные до недели, по
     гистограмме в 30 днях и архиве). */

  var MOCK_HIST = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000];
  var MOCK_CLIENTS = ['opencode/2.2.6', 'openclaw/0.9.1', 'hermes/1.2'];
  // серверы и инструменты — из состава мок-наборов: фильтр не должен предлагать
  // имя, которого нет ни в одном срезе
  var MOCK_TOOLS = {
    main: [['serena', ['find_symbol', 'find_referencing_symbols']], ['socraticode', ['codebase_search']],
      ['xerj', ['xerj_search']], ['context7', ['resolve-library-id', 'query-docs']], ['tavily', ['web_search']],
      ['playwright', ['browser_navigate', 'browser_take_screenshot']], ['raven', ['audit_page', 'score_page']],
      ['designer-skill', ['dispatch_intent', 'anti_slop_checklist']], ['gitea', ['list_issues']],
      ['portainer', ['docker_ps']], ['open-design', ['get_project']]],
    demo: [['fetch', ['fetch']], ['time', ['get_current_time']], ['memory', ['create_entities']],
      ['context7', ['query-docs']]],
    ops: [['fs', ['read_file', 'list_directory']], ['fetch', ['fetch']], ['gitea', ['list_pulls']]],
    dev: [['memory', ['read_graph', 'search_nodes']], ['context7', ['query-docs']], ['fs', ['read_file']]],
    lab: [['memory', ['search_nodes']], ['context7', ['resolve-library-id']]]
  };
  var MOCK_WEIGHT = [['main', 55], ['demo', 15], ['ops', 12], ['dev', 12], ['lab', 6]];
  var mockRows = null;

  // 120 дней истории: короче — архив пуст, длиннее — браузер считает впустую
  function mockRowsOf() {
    if (mockRows) { return mockRows; }
    var seed = 20261108;
    // mulberry32: демо обязано быть одинаковым при каждой перезагрузке, иначе
    // цифры прыгают на пустом месте и это выглядит как баг. Обычный LCG на
    // double теряет младшие биты (произведение не влезает в 53 бита)
    var rnd = function () {
      seed = (seed + 0x6D2B79F5) >>> 0;
      var t = seed;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    var pick = function (a) { return a[Math.floor(rnd() * a.length) % a.length]; };
    var now = Date.now();
    var rows = new Array(12000);
    for (var i = 0; i < rows.length; i++) {
      var w = rnd() * 100, set = MOCK_WEIGHT[0][0], acc = 0;
      for (var k = 0; k < MOCK_WEIGHT.length; k++) {
        acc += MOCK_WEIGHT[k][1];
        if (w < acc) { set = MOCK_WEIGHT[k][0]; break; }
      }
      var pair = pick(MOCK_TOOLS[set]);
      // латентность логнормальная с редким тяжёлым хвостом: p50 и p95 видно на глаз
      var ms = Math.round(Math.exp(4 + rnd() * 2.6) * (rnd() < 0.04 ? 5 : 1));
      rows[i] = {
        time: new Date(now - Math.floor(rnd() * 120 * 864e5)).toISOString(),
        set: set,
        server: pair[0],
        tool: pick(pair[1]),
        client: pick(MOCK_CLIENTS),
        ms: ms,
        failed: rnd() < 0.04
      };
    }
    mockRows = rows;
    return rows;
  }

  function mockHistIndex(ms) {
    for (var i = 0; i < MOCK_HIST.length; i++) { if (ms <= MOCK_HIST[i]) { return i; } }
    return MOCK_HIST.length;
  }

  function mockHistMid(i) {
    if (i >= MOCK_HIST.length) {
      var top = MOCK_HIST[MOCK_HIST.length - 1] + 1;
      return top + Math.floor(top / 2);
    }
    var lo = i > 0 ? MOCK_HIST[i - 1] + 1 : 0;
    return Math.floor((lo + MOCK_HIST[i]) / 2);
  }

  function mockAccum() { return { calls: 0, failures: 0, totalMs: 0, exact: [], hist: [] }; }

  function mockAdd(acc, r, approx) {
    acc.calls++;
    acc.totalMs += r.ms;
    if (r.failed) { acc.failures++; }
    if (approx) {
      var i = mockHistIndex(r.ms);
      acc.hist[i] = (acc.hist[i] || 0) + 1;
    } else {
      acc.exact.push(r.ms);
    }
  }

  function mockPercentile(acc, q, approx) {
    if (!acc.calls) { return 0; }
    if (approx) {
      var rank = Math.floor(q * (acc.calls - 1)), cum = 0;
      for (var i = 0; i < acc.hist.length; i++) {
        cum += acc.hist[i] || 0;
        if (cum > rank) { return mockHistMid(i); }
      }
      return mockHistMid(MOCK_HIST.length);
    }
    var xs = acc.exact.slice().sort(function (a, b) { return a - b; });
    return xs[Math.floor(q * (xs.length - 1))];
  }

  function mockRow(acc, approx) {
    return {
      calls: acc.calls,
      failures: acc.failures,
      p50ms: mockPercentile(acc, 0.5, approx),
      p95ms: mockPercentile(acc, 0.95, approx)
    };
  }

  function mockStats(path) {
    var q = {};
    (path.split('?')[1] || '').split('&').forEach(function (kv) {
      if (!kv) { return; }
      var i = kv.indexOf('=');
      if (i < 0) { q[kv] = ''; } else { q[kv.slice(0, i)] = decodeURIComponent(kv.slice(i + 1)); }
    });
    var period = q.period || 'today';
    var approx = period === '30d' || period === 'archive';
    var step = q.bucket === 'month' ? 'month' : q.bucket === 'day' ? 'day'
      : period === '30d' ? 'day' : period === 'archive' ? 'month' : 'hour';

    var now = new Date();
    var day = function (shift) {
      return new Date(now.getFullYear(), now.getMonth(), now.getDate() - (shift || 0)).getTime();
    };
    // окна как в QueryHistory: неделя и месяц включительно, архив — всё до них
    var from = period === 'today' ? day(0) : period === '7d' ? day(6) : period === '30d' ? day(29) : 0;
    var to = period === 'archive' ? day(29) : now.getTime();
    var start = function (t) {
      var d = new Date(t);
      if (step === 'hour') { return new Date(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours()).toISOString(); }
      if (step === 'day') { return new Date(d.getFullYear(), d.getMonth(), d.getDate()).toISOString(); }
      return new Date(d.getFullYear(), d.getMonth(), 1).toISOString();
    };

    var totals = mockAccum(), series = {}, tools = {}, servers = {}, clients = {}, sets = {};
    var first = 0;
    var at = function (map, key) { return map[key] || (map[key] = mockAccum()); };

    mockRowsOf().forEach(function (r) {
      var t = Date.parse(r.time);
      if (t < from || t > to) { return; }
      // bySet считается до фильтров среза: колонка в таблице наборов показывает
      // все наборы, а не открытый
      mockAdd(at(sets, r.set), r, false);
      if (q.server && r.server !== q.server) { return; }
      if (q.tool && r.tool !== q.tool) { return; }
      if (q.client && r.client !== q.client) { return; }
      if (q.set && q.set !== 'all' && r.set !== q.set) { return; }
      if (!first || t < first) { first = t; }
      mockAdd(totals, r, approx);
      mockAdd(at(tools, r.server + '\u0000' + r.tool), r, approx);
      mockAdd(at(servers, r.server), r, approx);
      mockAdd(at(clients, r.client), r, approx);
      mockAdd(at(series, start(t)), r, approx);
    });

    var list = function (map, key) {
      return Object.keys(map).sort().map(function (k) {
        var row = mockRow(map[k], approx);
        row[key] = k;
        return row;
      });
    };
    var toolRows = Object.keys(tools).map(function (k) {
      var parts = k.split('\u0000');
      var row = mockRow(tools[k], approx);
      row.server = parts[0];
      row.tool = parts[1];
      return row;
    }).sort(function (a, b) {
      if (a.server !== b.server) { return a.server < b.server ? -1 : 1; }
      return a.tool < b.tool ? -1 : a.tool > b.tool ? 1 : 0;
    });

    return {
      period: period,
      from: new Date(first || from).toISOString(),
      to: new Date(to).toISOString(),
      approximate: approx,
      totals: mockRow(totals, approx),
      series: Object.keys(series).sort().map(function (t) {
        var row = mockRow(series[t], approx);
        row.t = t;
        return row;
      }),
      tools: toolRows,
      servers: list(servers, 'server'),
      clients: list(clients, 'client'),
      bySet: Object.keys(sets).sort().map(function (k) {
        return { set: k, calls: sets[k].calls, failures: sets[k].failures, totalMs: sets[k].totalMs };
      }),
      unreachable: []
    };
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
    var setsW = word(st.sets.length, 'set');
    var srvW = word(st.servers.length, 'server');
    $('c-sets').textContent = fmtN(st.sets.length);
    $('c-servers').textContent = fmtN(st.servers.length);
    $('c-running').textContent = fmtN(running);
    $('f-sets').textContent = fmtN(st.sets.length);
    $('f-servers').textContent = fmtN(st.servers.length);
    $('f-running').textContent = fmtN(running);
    $('c-sets-w').textContent = setsW;
    $('c-servers-w').textContent = srvW;
    $('c-running-w').textContent = t('top.runningW');
    $('f-sets-w').textContent = setsW;
    $('f-servers-w').textContent = srvW;
    $('f-running-w').textContent = t('top.runningW');
    $('f-updated').textContent = t('top.updated', { time: clock() });
    $('rail-count').textContent = fmtN(st.sets.length);
    $('sets-count').textContent = fmtN(st.sets.length);

    var none = st.sets.length === 0;
    show($('empty-state'), none);
    show($('rail-empty'), none);
    show($('phead'), !none);
    show($('tabs'), !none);
    show($('region'), !none);
    show($('kpis'), !none);
    overviewBlocks();
    if (none) { renderKpis(null); renderServers(null); renderLog([]); renderSetServers(); }
  }

  // Наборы уже в рельсе: на странице набора второй их список ничего не добавляет.
  // Поэтому там — серверы этого набора, а список наборов живёт только на обзоре.
  function overviewBlocks() {
    var none = st.sets.length === 0;
    show($('sets-block'), !none && !st.sel);
    show($('set-srv-block'), !none && !!st.sel);
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
        var names = s.servers || [];
        if (state.cls === 'ok') {
          var n = names.length;
          rate.textContent = n + ' ' + word(n, 'server');
        } else {
          rate.textContent = state.text;
        }
        var label = t('rail.item', { name: name, port: s.port, state: state.text });
        a.title = label + (names.length ? '\n' + names.join(', ') : '');
        a.setAttribute('aria-label', label);
      });
  }

  // Вызовы и ошибки по наборам за выбранный период: их отдаёт /api/stats?set=all
  // полем bySet. Старый демон поля не знает — тогда честный прочерк.
  function bySetCounters() {
    var d = st.st.data;
    if (!d || !Array.isArray(d.bySet)) { return null; }
    var map = {};
    d.bySet.forEach(function (r) { map[r.set] = r; });
    return map;
  }

  // Счётчики выбранного набора для полосы KPI: они живут в его процессе.
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
    box.textContent = names.length ? names.join(' · ') : t('srv.noServers');
  }

  function renderSetsTable() {
    var per = bySetCounters();
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
        cell.title = names.length ? names.join(', ') : t('srv.noServers');
        tr.children[1].textContent = s.port;
        pill.querySelector('.dot').className = 'dot ' + state.cls;
        pill.lastChild.textContent = state.text;
        tr.children[3].textContent = names.length;
        tr.children[4].textContent = uptime(s);
        var row = per ? per[s.id] : null;
        tr.children[5].textContent = per ? fmtN(row ? row.calls : 0) : '—';
        tr.children[6].textContent = per ? pct(row ? row.failures : 0, row ? row.calls : 0) : '—';
        tr.dataset.id = s.id;
      });
  }

  /* ---------- выбранный набор ---------- */

  // Состав выбранного набора с состоянием и изоляцией: данные уже в st.sets[sel]
  // и st.health, отдельного запроса не нужно.
  function renderSetServers() {
    var set = current();
    var names = (set && set.servers) || [];
    var health = (st.health && st.health.servers) || [];
    var rows = names.map(function (name) {
      return {
        name: name,
        srv: st.servers.filter(function (s) { return s.name === name; })[0] || null,
        health: health.filter(function (x) { return x.name === name; })[0] || null
      };
    });
    $('set-srv-count').textContent = fmtN(rows.length);
    show($('set-srv-empty'), rows.length === 0);
    sync($('set-srv-body'), rows, function (r) { return r.name; },
      function () {
        var tr = el('tr', 'is-click');
        var nm = tr.appendChild(el('td', 'tb-name'));
        nm.appendChild(el('span', 'setlabel'));
        tr.appendChild(el('td', 'hide-sm'));
        var cell = tr.appendChild(el('td'));
        var pill = cell.appendChild(el('span', 'state'));
        pill.appendChild(el('span', 'dot'));
        pill.appendChild(document.createTextNode(' '));
        pill.appendChild(el('span'));
        tr.appendChild(el('td', 'hide-md'));
        tr.appendChild(el('td', 'hide-sm'));
        return tr;
      },
      function (tr, row) {
        var h = row.health;
        var cls = 'warn';
        var note = h ? t('srv.unchecked') : t('srv.setOff');
        if (h && h.healthy === true && !h.unknown) { cls = 'ok'; note = t('srv.ok2'); }
        if (h && h.healthy === false) { cls = 'err'; note = t('srv.noResponse'); }
        tr.children[0].firstChild.textContent = row.name;
        tr.children[0].title = row.name;
        tr.children[1].textContent = (row.srv && row.srv.transport) || '—';
        var pill = tr.children[2].querySelector('.state');
        pill.querySelector('.dot').className = 'dot ' + cls;
        pill.lastChild.textContent = note;
        pill.title = (h && h.error) || '';
        var iso = isoOf(h && h.isolation);
        var isoTd = tr.children[3];
        isoTd.replaceChildren(iso ? el('span', 'badge', iso.label) : document.createTextNode('—'));
        isoTd.title = iso ? iso.title : '';
        tr.children[4].textContent = (row.srv && row.srv.tags && row.srv.tags.length) ? row.srv.tags.join(', ') : '—';
        tr.dataset.name = row.name;
      });
  }

  function current() {
    for (var i = 0; i < st.sets.length; i++) {
      if (st.sets[i].id === st.sel) { return st.sets[i]; }
    }
    return null;
  }

  function select(id, force) {
    if (id === st.sel && !force) {
      // клик по уже выбранному набору возвращает к нему статистику, если фильтр увели руками
      if (st.st.set !== (id || '')) {
        st.st.set = id || '';
        fillFilters();
        if (st.tab === 'stats') { loadStats(); }
      }
      renderHead();
      return;
    }
    // форма сервера привязана к списку прежнего набора — закрываем без возврата фокуса
    closeSrv(false);
    st.sel = id;
    st.health = null;
    st.calls = null;
    st.logKey = '';
    // статистика идёт за рельсом: свой фильтр набора начинается с текущего,
    // ручной выбор в списке фильтра живёт до следующего переключения набора
    st.st.set = id || '';
    // адрес повторяет открытое: на статистике его держит setTab, набор его не перебивает
    if (st.tab !== 'stats') {
      if (id) {
        history.replaceState(null, '', '#/set/' + id);
      } else {
        history.replaceState(null, '', location.pathname);
      }
    }
    setTitle();
    renderRail();
    overviewBlocks();
    renderSetServers();
    renderHead();
    renderServers(null);
    renderCalls(null);
    renderLog([]);
    fillFilters();
    if (id) { loadHealth(); loadCalls(); loadLog(); }
    if (st.tab === 'stats') { loadStats(); }
  }

  function setTitle() {
    document.title = st.sel ? t('title.set', { name: st.sel }) : t('title.panel');
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
    $('set-name').textContent = t('head.setName', { name: set.name || set.id });
    $('set-port').textContent = ':' + set.port;
    var pill = $('set-state');
    pill.replaceChildren(el('span', 'dot ' + state.cls), document.createTextNode(state.text));
    $('endpoint').textContent = 'http://' + (location.hostname || '127.0.0.1') + ':' + set.port + '/mcp';
    $('key').textContent = set.keyPrefix ? set.keyPrefix + '…' : (set.open ? t('head.keyOpen') : t('head.noKey'));
    $('schema').textContent = schemaLine(st.calls && st.calls.schema);
    $('run').textContent = set.runtime && set.runtime.running ? t('head.restart') : t('head.start');
    $('dlg-set').textContent = set.name || set.id;

    var r = set.runtime || {};
    var bad = !r.running && (r.error || (r.exitCode != null && r.exitCode > 0));
    show($('set-error'), !!bad);
    if (bad) {
      $('set-error-h').textContent = r.error ? t('head.stopped') : t('head.exited', { code: r.exitCode });
      var lines = [];
      // текст ошибки процесса даёт демон: панель его не переводит
      if (r.error) { lines.push(r.error); }
      if (r.exitCode != null) { lines.push(t('head.exitCode', { code: r.exitCode })); }
      if (r.restarts) { lines.push(t('head.restarts', { n: r.restarts })); }
      $('set-error-d').textContent = lines.join('\n');
    }
  }

  function renderKpis(data) {
    var agg = data ? aggregated() : null;
    var pctls = data ? tailPercentiles(data.calls || []) : { p50: null, p95: null };
    $('k-calls').textContent = agg ? fmtN(agg.calls) : '—';
    $('k-calls-w').textContent = agg ? word(agg.calls, 'call') : t('kpi.calls');
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
    var inSet = (set && set.servers) || [];
    // список — общий реестр хоста, а не состав набора: новый сервер виден сразу,
    // а принадлежность к набору помечена в строке
    var rows = st.servers.map(function (s) {
      var h = null;
      if (health && health.servers) {
        h = health.servers.filter(function (x) { return x.name === s.name; })[0] || null;
      }
      return { name: s.name, health: h, mine: inSet.indexOf(s.name) >= 0 };
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
        var iso = top.appendChild(el('span', 'iso'));
        iso.appendChild(shield());
        iso.appendChild(el('span', 'iso-t'));
        var foot = li.appendChild(el('div', 'srv-foot'));
        foot.appendChild(el('span', 'srv-note'));
        var edit = foot.appendChild(el('button', 'mini srv-edit'));
        edit.type = 'button';
        edit.dataset.act = 'edit';
        var probe = foot.appendChild(el('button', 'mini'));
        probe.type = 'button';
        probe.dataset.act = 'probe';
        return li;
      },
      function (li, row) {
        var srv = st.servers.filter(function (s) { return s.name === row.name; })[0];
        var h = row.health;
        var cls = 'warn';
        // причина ошибки приходит от демона по-русски — её не переводим
        var note = row.mine ? t('srv.notProbed') : t('srv.notInSet');
        if (row.mine && health && !health.running) { note = t('srv.setOff'); }
        if (row.mine && h && h.healthy === true && h.unknown) { cls = 'warn'; note = t('srv.unchecked'); }
        if (row.mine && h && h.healthy === true && !h.unknown) { cls = 'ok'; note = t('srv.ok'); }
        if (row.mine && h && h.healthy === false) { cls = 'err'; note = h.error || t('srv.noResponse'); }
        li.querySelector('.dot').className = 'dot ' + cls;
        li.querySelector('.snm').textContent = row.name;
        li.querySelector('.snm').title = row.name;
        var kind = (srv && srv.transport) || (srv && srv.command ? 'stdio' : srv && srv.url ? 'http' : '—');
        li.querySelector('.badge').textContent = kind;
        // изоляция живёт в health: у серверов вне набора её нет, и это честно
        var iso = isoOf(h && h.isolation);
        var ib = li.querySelector('.iso');
        show(ib, !!iso);
        if (iso) {
          ib.className = 'iso ' + iso.cls;
          ib.title = iso.title;
          ib.setAttribute('aria-label', iso.title);
          // тревогу видно и с клавиатуры; норма (bwrap) не отнимает tab-стоп
          ib.tabIndex = iso.cls === 'iso-ok' ? -1 : 0;
          ib.querySelector('.iso-t').textContent = iso.label;
        }
        var n = li.querySelector('.srv-note');
        n.textContent = note;
        n.title = note;
        n.classList.toggle('is-err', cls === 'err');
        li.querySelector('[data-act=probe]').dataset.server = row.name;
        li.querySelector('[data-act=probe]').textContent = t('srv.probe');
        li.querySelector('[data-act=edit]').dataset.edit = row.name;
        li.querySelector('[data-act=edit]').textContent = t('srv.edit');
      });
  }

  /* ---------- редактор сервера ----------
     Реестр общий для хоста, сервер правится там же, где список: форма накрывает
     панель. Отдельный экран в узком дровере потерял бы и набор, и контекст. */

  function srvForm() { return $('srvform'); }

  function srvErr(msg) {
    var box = $('srv-err');
    box.textContent = msg || '';
    show(box, !!msg);
  }

  // закрытие: фокус возвращается на кнопку, которая форму открыла
  function closeSrv(back) {
    if (srvForm().hidden) { return; }
    srvForm().hidden = true;
    var target = back === false ? null : st.srvReturn;
    st.srvReturn = null;
    if (target && document.contains(target) && target.offsetParent !== null) { target.focus(); }
    else if (back !== false) { $('srv-add').focus(); }
  }

  function srvWhen() {
    var f = srvForm();
    var t = f.elements.transport.value;
    [].forEach.call(f.querySelectorAll('[data-when]'), function (node) {
      node.hidden = node.dataset.when !== t;
    });
  }

  function fillSrv(srv) {
    var f = srvForm();
    var v = function (n, val) { f.elements[n].value = val == null ? '' : String(val); };
    v('name', srv ? srv.name : '');
    f.elements.name.readOnly = !!srv;
    v('description', srv && srv.description);
    f.elements.transport.value = (srv && srv.transport === 'http') ? 'http' : 'stdio';
    v('command', srv && srv.command ? srv.command.join('\n') : '');
    v('cwd', srv && srv.cwd);
    v('url', srv && srv.url);
    v('headers', srv && srv.headers ? Object.keys(srv.headers).map(function (k) {
      return k + ': ' + srv.headers[k];
    }).join('\n') : '');
    v('env', srv && srv.env ? Object.keys(srv.env).map(function (k) {
      return k + '=' + srv.env[k];
    }).join('\n') : '');
    v('envAllow', srv && srv.envAllow ? srv.envAllow.join('\n') : '');
    v('tags', srv && srv.tags ? srv.tags.join(', ') : '');
    f.elements.enabled.checked = !srv || srv.enabled !== false;
    var sb = (srv && srv.sandbox) || {};
    v('sbMode', sb.mode);
    v('sbOnUnavailable', sb.onUnavailable);
    v('sbNetwork', sb.network);
    v('sbRo', sb.ro ? sb.ro.join('\n') : '');
    v('sbRw', sb.rw ? sb.rw.join('\n') : '');
    v('sbMemoryMb', sb.memoryMb);
    v('sbPids', sb.pids);
    v('sbTmpMb', sb.tmpMb);
    v('sbTimeoutSec', sb.timeoutSec);
    srvWhen();
  }

  function linesOf(text) {
    return (text || '').split('\n').map(function (s) { return s.trim(); })
      .filter(function (s) { return s; });
  }

  function mapOf(text, sep) {
    var out = {};
    linesOf(text).forEach(function (line) {
      var i = line.indexOf(sep);
      if (i > 0) { out[line.slice(0, i).trim()] = line.slice(i + 1).trim(); }
    });
    return out;
  }

  function collectSrv() {
    var f = srvForm();
    var val = function (n) { return (f.elements[n].value || '').trim(); };
    var transport = f.elements.transport.value === 'http' ? 'http' : 'stdio';
    var out = { name: val('name'), transport: transport, enabled: f.elements.enabled.checked };
    if (val('description')) { out.description = val('description'); }
    if (transport === 'http') {
      out.url = val('url');
      var headers = mapOf(val('headers'), ':');
      if (Object.keys(headers).length) { out.headers = headers; }
    } else {
      out.command = linesOf(val('command'));
      if (val('cwd')) { out.cwd = val('cwd'); }
    }
    var env = mapOf(val('env'), '=');
    if (Object.keys(env).length) { out.env = env; }
    var allow = linesOf(val('envAllow'));
    if (allow.length) { out.envAllow = allow; }
    var tags = val('tags').split(',').map(function (s) { return s.trim(); })
      .filter(function (s) { return s; });
    if (tags.length) { out.tags = tags; }
    // песочница живёт только у stdio: у http её нет по контракту
    if (transport === 'stdio') {
      var sb = {};
      if (val('sbMode')) { sb.mode = val('sbMode'); }
      if (val('sbOnUnavailable')) { sb.onUnavailable = val('sbOnUnavailable'); }
      if (val('sbNetwork')) { sb.network = val('sbNetwork'); }
      var ro = linesOf(val('sbRo'));
      var rw = linesOf(val('sbRw'));
      if (ro.length) { sb.ro = ro; }
      if (rw.length) { sb.rw = rw; }
      [['sbMemoryMb', 'memoryMb'], ['sbPids', 'pids'], ['sbTmpMb', 'tmpMb'], ['sbTimeoutSec', 'timeoutSec']]
        .forEach(function (pair) {
          var n = parseInt(val(pair[0]), 10);
          if (!isNaN(n)) { sb[pair[1]] = n; }
        });
      if (Object.keys(sb).length) { out.sandbox = sb; }
    }
    return out;
  }

  function openSrv(name) {
    var srv = name ? st.servers.filter(function (s) { return s.name === name; })[0] : null;
    if (name && !srv) { fail(t('srv.notFound', { name: name })); return; }
    st.srvEditing = name || '';
    st.srvReturn = document.activeElement;
    fillSrv(srv);
    srvErr('');
    $('srv-del').hidden = !name;
    $('srvform-h').textContent = name ? t('form.editing', { name: name }) : t('form.new');
    $('srv-save').textContent = name ? t('common.save') : t('form.create');
    show(srvForm(), true);
    // у существующего сервера имя не редактируется — фокус на описании
    srvForm().elements[srv ? 'description' : 'name'].focus();
  }

  function saveSrv(e) {
    e.preventDefault();
    if (st.busy) { return; }
    var body = collectSrv();
    var editing = st.srvEditing;
    var btn = $('srv-save');
    st.busy = true;
    btn.disabled = true;
    srvErr('');
    // текст ошибки даёт бэкенд: он точнее любого предсказания в браузере
    send(editing ? 'PUT' : 'POST', editing ? '/api/servers/' + encodeURIComponent(editing) : '/api/servers', body)
      .then(function () { return loadState(); })
      // loadState перерисовывает наборы и шапку, но не список правой панели
      .then(function () { renderServers(st.health); })
      .then(function () { closeSrv(); })
      .catch(function (err) { srvErr(err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  function delSrv() {
    var name = st.srvEditing;
    if (!name || st.busy) { return; }
    var ok = window.confirm(t('form.deleteConfirm', { name: name }));
    if (!ok) { return; }
    var btn = $('srv-del');
    st.busy = true;
    btn.disabled = true;
    srvErr('');
    send('DELETE', '/api/servers/' + encodeURIComponent(name))
      .then(function () { return loadState(); })
      .then(function () { renderServers(st.health); })
      .then(function () {
        closeSrv(false);
        $('srv-add').focus();
      })
      .catch(function (err) { srvErr(err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
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
    $('tail-span').textContent = data.calls && data.calls.length ? t('tail.span', { n: fmtN(Math.min(8, data.calls.length)) }) : '—';
    var frag = document.createDocumentFragment();
    rows.forEach(function (c) {
      var tr = el('tr', 'is-click');
      tr.appendChild(el('td', 'n', clock(c.time)));
      tr.appendChild(el('td', 'hide-xs', c.server || '—'));
      var tool = tr.appendChild(el('td', 'mono'));
      tool.appendChild(el('span', 'od-truncate', c.tool || '—'));
      tool.title = c.tool || '';
      var dur = tr.appendChild(el('td', 'num n hide-sm' + (c.ms > 1000 ? ' slow' : ''), fmtN(c.ms)));
      dur.title = c.client || '';
      var stc = tr.appendChild(el('td', 'num st n' + (c.failed ? ' bad' : ''), c.failed ? t('calls.bad') : t('calls.ok')));
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
      $('calls-note').textContent = t('calls.unavailable');
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
      var dur = tr.appendChild(el('td', 'num n' + (c.ms > 1000 ? ' slow' : ''), fmtN(c.ms)));
      dur.classList.add('hide-xs');
      var stc = tr.appendChild(el('td', 'num st n' + (c.failed ? ' bad' : ''), c.failed ? t('calls.bad') : t('calls.ok')));
      stc.title = c.error || '';
      frag.appendChild(tr);
    });
    box.replaceChildren(frag);
    if (scroll) { scroll.scrollTop = keep; }

    $('calls-count').textContent = rows.length === all.length
      ? fmtN(all.length) : t('calls.of', { n: fmtN(rows.length), m: fmtN(all.length) });
    var empty = rows.length === 0;
    show($('calls-note'), empty);
    $('calls-note').textContent = !all.length ? t('tail.empty') : t('calls.noMatch');
  }

  function loadEnv() {
    return get('/api/env').then(function (data) {
      if (!data) { return; }
      st.env = data.vars || [];
      st.envRemoteError = data.remoteError || '';
      renderEnv();
    }).catch(function (e) { fail(e.message, e); });
  }

  // отдельно от запроса: смена языка перерисовывает подписи без похода в демон
  function renderEnv() {
    if (!st.env) { return; }
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
        li.querySelector('.var-val').textContent = (v.set ? t('env.set') : t('env.unset')) +
          (v.source ? ' · ' + v.source : '') +
          (!v.set && st.envRemoteError ? ' · ' + st.envRemoteError : '');
      });
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
  var PERIOD_KEYS = { today: 'stats.today', '7d': 'stats.7d', '30d': 'stats.30d', archive: 'stats.archive' };
  function periodName(p) { return t(PERIOD_KEYS[p] || 'stats.today'); }

  // корзина среза: час на день, день на неделю/месяц, месяц на архив —
  // иначе архив рисуется сотнями дневных столбиков
  function bucketOf(period) {
    return period === 'today' ? 'hour' : period === 'archive' ? 'month' : 'day';
  }

  function statsPath(filters) {
    var p = '/api/stats?set=' + encodeURIComponent(st.st.set || 'all') +
      '&period=' + st.st.period + '&bucket=' + bucketOf(st.st.period);
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
    // до первой загрузки /api/state набор неизвестен: запрос «все наборы» на старте
    // дал бы чужие числа и лишний запрос — срез приедет после select()
    if (!st.sets.length) { return Promise.resolve(); }
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
        // выбор, которого нет в новом наборе, снимаем: иначе список показывает
        // «все», а запрос продолжает фильтровать старым значением
        if (st.st.server && st.st.facets.servers.indexOf(st.st.server) < 0) { st.st.server = ''; }
        if (st.st.tool && st.st.facets.tools.indexOf(st.st.tool) < 0) { st.st.tool = ''; }
        if (st.st.client && st.st.facets.clients.indexOf(st.st.client) < 0) { st.st.client = ''; }
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
    $('stats-empty-why').textContent = t('stats.fail', { msg: e.message });
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
    // язык в ключе: подпись «все» тоже пересобирается при переключении
    var key = lang + '\u0001' + (values || []).join('\u0001');
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
    fillSelect($('s-set'), st.sets.map(function (s) { return s.id; }), st.st.set, t('stats.allSets'), setName);
    fillSelect($('s-server'), f.servers, st.st.server, t('stats.all'));
    fillSelect($('s-tool'), f.tools, st.st.tool, t('stats.all'));
    fillSelect($('s-client'), f.clients, st.st.client, t('stats.all'));
  }

  function setName(id) {
    for (var i = 0; i < st.sets.length; i++) {
      if (st.sets[i].id === id) { return st.sets[i].name || id; }
    }
    return id;
  }

  function renderStats(d) {
    // колонка «вызовов» в таблице наборов считается из этого же ответа (bySet)
    renderSetsTable();
    var tot = (d && d.totals) || { calls: 0 };
    var empty = !tot.calls;
    show($('stats-empty'), empty);
    show($('s-kpis'), !empty);
    show($('s-charts'), !empty);

    var unreach = (d && d.unreachable) || [];
    show($('stats-unreach'), unreach.length > 0);
    $('stats-unreach').textContent = unreach.length ? t('stats.unreach', { list: unreach.join(', ') }) : '';

    if (empty) {
      $('stats-empty-why').textContent = (st.st.server || st.st.tool || st.st.client)
        ? t('stats.emptyFilters')
        : t('stats.emptyPeriod', { p: periodName(st.st.period) });
      return;
    }

    $('s-calls').textContent = fmtN(tot.calls);
    $('s-calls-w').textContent = word(tot.calls, 'call');
    $('s-fail').textContent = pct(tot.failures, tot.calls);
    $('s-p50').textContent = ms(tot.p50ms);
    $('s-p95').textContent = ms(tot.p95ms);
    show($('s-p50-a'), !!d.approximate);
    show($('s-p95-a'), !!d.approximate);
    // процентили разрезов считаются по тем же гистограммам — помечаем их так же
    [].forEach.call(document.querySelectorAll('.ph-pct'), function (th) {
      th.textContent = d.approximate ? th.dataset.k + ' ≈' : th.dataset.k;
      if (d.approximate) { th.title = t('stats.approxTip'); } else { th.removeAttribute('title'); }
    });

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
      var td = rest.appendChild(el('td', '', t('stats.more', { n: fmtN(list.length - limit) })));
      td.colSpan = 5;
    }
    tbody.replaceChildren(frag);
  }

  // series — вызовы по корзинам периода (час для «сегодня», день для остальных).
  function renderSeries(rows) {
    var box = $('activity-bars');
    var hour = st.st.period === 'today';
    var monthly = st.st.period === 'archive';
    var label = hour ? function (iso) { return hm(new Date(iso).getTime()); } : monthly ? mon : day;
    show($('activity-empty'), rows.length === 0);
    $('s-activity-title').textContent = hour ? t('stats.byHour') : monthly ? t('stats.byMonth') : t('stats.byDay');
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
      col.title = err
        ? t('stats.colTipErr', { label: label(r.t), n: fmtN(n), err: fmtN(err) })
        : t('stats.colTip', { label: label(r.t), n: fmtN(n) });
      frag.appendChild(col);
    });
    box.replaceChildren(frag);

    $('s-span').textContent = fmtN(calls) + ' ' + word(calls, 'call') + ' · ' + rows.length + ' ' +
      (hour ? t('unit.hour') : monthly ? t('unit.month') : t('unit.day'));
    $('s-from').textContent = label(rows[0].t);
    $('s-to').textContent = label(rows[rows.length - 1].t);
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
    // срез не перезапрашиваем на каждый клик: тикер обновляет его и так
    if (name === 'stats' && !st.st.data) { loadStats(); }
  }

  /* Стрелки/Home/End внутри группы кнопок: roving tabindex сам по себе оставлял
     недостижимыми всё, кроме выбранного (Tab ходит только по tabIndex=0). */
  function arrowNav(e, group, pick) {
    if (['ArrowRight', 'ArrowLeft', 'Home', 'End'].indexOf(e.key) < 0) { return; }
    var btns = [].slice.call(group.querySelectorAll('.segbtn'));
    var i = btns.indexOf(document.activeElement);
    if (i < 0) { return; }
    var j = e.key === 'Home' ? 0 : e.key === 'End' ? btns.length - 1
      : (i + (e.key === 'ArrowRight' ? 1 : -1) + btns.length) % btns.length;
    e.preventDefault();
    pick(btns[j]);
    btns[j].focus();
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
    var drawer = window.innerWidth < 1200;
    st.sideReturn = document.activeElement;
    document.body.classList.add('side-open');
    $('side-open').setAttribute('aria-expanded', 'true');
    show($('scrim'), drawer);
    if (drawer) {
      var tab = document.querySelector('.seg .segbtn[aria-selected="true"]') || $('side-close');
      tab.focus();
    }
  }

  function closeSide() {
    var was = document.body.classList.contains('side-open');
    closeSrv(false);
    document.body.classList.remove('side-open');
    $('side-open').setAttribute('aria-expanded', 'false');
    show($('scrim'), false);
    // фокус не должен оставаться в скрытой панели: возвращаем туда, откуда открыли,
    // а если оттуда некуда (открыли программно) — на саму кнопку «Детали»
    var back = st.sideReturn && document.contains(st.sideReturn) && st.sideReturn.offsetParent !== null
      ? st.sideReturn : ($('side-open').offsetParent !== null ? $('side-open') : null);
    st.sideReturn = null;
    if (was && back) { back.focus(); }
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
    btn.setAttribute('aria-label', compact ? t('rail.expand') : t('rail.collapse'));
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
        var b = td.appendChild(el('button', 'mini'));
        b.type = 'button';
        return tr;
      },
      function (tr, k) {
        var revoked = !!k.revokedAt;
        tr.children[0].textContent = (set.keyPrefix || '—') + '…';
        tr.children[1].textContent = day(k.createdAt);
        tr.children[2].textContent = revoked ? t('keys.revoked', { date: day(k.revokedAt) }) : t('keys.active');
        tr.children[2].className = revoked ? 'st' : '';
        tr.children[3].textContent = k.note || '—';
        var btn = tr.children[4].querySelector('button');
        btn.textContent = t('keys.revoke');
        btn.dataset.key = k.id;
        btn.hidden = revoked;
        tr.classList.toggle('row-warn', revoked);
      });
  }

  /* ---------- язык ----------
     Переключение перерисовывает всё, что панель собрала сама: статику обходит
     applyStatic, данные — заново теми же render-функциями. */
  var relang = function () {
    applyStatic();
    setTitle();
    renderTop();
    renderRail();
    renderSetsTable();
    renderSetServers();
    renderHead();
    renderServers(st.health);
    renderTail(st.calls);
    renderCalls(st.calls);
    renderEnv();
    renderKeys(current() || {});
    fillFilters();
    renderStats(st.st.data);
    applyRail();
  };

  /* ---------- привязки ---------- */

  $('rail-sets').addEventListener('click', function (e) {
    var row = e.target.closest('.setrow');
    if (!row) { return; }
    e.preventDefault();
    select(row.dataset.id);
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
  document.querySelector('.seg').addEventListener('keydown', function (e) {
    arrowNav(e, this, function (b) { setSideTab(b.dataset.side); });
  });
  $('s-period').addEventListener('keydown', function (e) {
    arrowNav(e, this, function (b) { setPeriod(b.dataset.period); });
  });

  // Tab не уходит ни в контент под дровером, ни за открытую форму сервера
  $('side').addEventListener('keydown', function (e) {
    var formOpen = !srvForm().hidden;
    if (formOpen && e.key === 'Escape') {
      // Esc сначала закрывает форму: дровер под ней человек не закрывал
      e.stopPropagation();
      closeSrv();
      return;
    }
    if (e.key !== 'Tab' || !(formOpen || document.body.classList.contains('side-open'))) { return; }
    var root = formOpen ? srvForm() : this;
    var f = [].slice.call(root.querySelectorAll('a[href],button:not([disabled]),[tabindex]:not([tabindex="-1"])'))
      .filter(function (n) { return n.offsetParent !== null; });
    if (!f.length) { return; }
    var first = f[0], last = f[f.length - 1], at = document.activeElement;
    if (e.shiftKey && (at === first || !this.contains(at))) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && at === last) { e.preventDefault(); first.focus(); }
  });

  $('srv-add').addEventListener('click', function () { openSrv(''); });
  $('srvform-close').addEventListener('click', function () { closeSrv(); });
  $('srvform').elements.transport.addEventListener('change', srvWhen);
  $('srvform').addEventListener('submit', saveSrv);
  $('srv-del').addEventListener('click', delSrv);

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

  // язык: дефолт английский, выбор живёт в localStorage и в <html lang>
  var langSel = $('lang');
  langSel.value = lang;
  langSel.addEventListener('change', function () {
    lang = langSel.value === 'ru' ? 'ru' : 'en';
    try { localStorage.setItem(LANG_KEY, lang); } catch (e) { /* приватный режим */ }
    relang();
  });

  // схема выбирает select в шапке; сам атрибут ставит theme.js до первой отрисовки
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
      btn.textContent = ok ? t('head.copied') : t('head.copyFail');
      setTimeout(function () { btn.textContent = t('head.copy'); }, 1200);
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
    var edit = e.target.closest('button[data-edit]');
    if (edit) { openSrv(edit.dataset.edit); return; }
    var btn = e.target.closest('button[data-server]');
    if (!btn) { return; }
    var note = btn.closest('.srv').querySelector('.srv-note');
    note.textContent = t('srv.probing');
    note.classList.remove('is-err');
    send('POST', '/api/servers/' + encodeURIComponent(btn.dataset.server) + '/probe').then(function (r) {
      note.textContent = r.ok
        ? t('srv.probeOk', { ms: fmtN(r.ms), n: fmtN((r.tools || []).length) })
        : (r.error || t('srv.noResponse')) + ' · ' + t('unit.ms', { n: fmtN(r.ms) });
      note.classList.toggle('is-err', !r.ok);
    }).catch(function () { note.textContent = t('srv.probeFail'); note.classList.add('is-err'); });
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
    if (!name) { err.textContent = t('add.nameRequired'); err.hidden = false; return; }
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
  applyStatic();
  setTitle();
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
