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
      'a11y.skip': 'Skip to content',
      'top.details': 'Details',
      'top.lang': 'Language',
      'top.theme': 'Color scheme',
      'top.updatedLabel': 'updated',
      'top.runningW': 'running',
      'rail.title': 'Sets',
      'rail.open': 'Open the set list',
      'rail.collapse': 'Collapse the set list',
      'rail.expand': 'Expand the set list',
      'rail.empty': 'no sets',
      'rail.add': 'New set',
      'rail.name': 'name',
      'rail.port': 'port',
      'rail.create': 'create',
      'rail.item': '{name} · port {port} · {state}',
      'common.cancel': 'cancel',
      'common.save': 'save',
      'common.close': 'close',
      'common.done': 'done',
      'empty.title': 'No sets yet',
      'empty.p': 'A set is one MCP endpoint with its own port, servers and keys.',
      'empty.add': 'Create the first set',
      'head.endpoint': 'Endpoint',
      'head.copy': 'copy',
      'head.copyTip': 'copy the MCP endpoint of this set',
      'head.copied': 'copied',
      'head.copyFail': 'failed',
      'head.key': 'Key',
      'head.keyTip': 'only the prefix is shown',
      'head.keyOpen': 'set is open',
      'head.copyKey': 'Copy key',
      'head.noKey': 'no key',
      'head.schemaTip': "how much context the set's tool list costs",
      'head.servers': 'Servers',
      'head.tools': 'Tools',
      'head.keys': 'Keys',
      'head.start': 'Start',
      'head.restart': 'Restart',
      'head.stop': 'Stop',
      'head.stopped': 'set stopped',
      'head.exited': 'process exited (code {code})',
      'head.exitCode': 'exit code {code}',
      'head.restarts': 'restarts: {n}',
      'tabs.overview': 'Overview',
      'tabs.calls': 'Calls',
      'tabs.stats': 'Statistics',
      'kpi.calls': 'calls · 7 days',
      'kpi.errRate': 'error rate',
      'kpi.p50': 'p50 latency',
      'kpi.p95': 'p95 latency',
      'setSrv.title': 'Servers',
      'setSrv.add': 'Add server',
      'setSrv.note': '{h} healthy · {f} failed · {u} not checked',
      'setSrv.checking': 'checking {n} {serversW}…',
      'setSrv.thServer': 'Server',
      'setSrv.thType': 'Type',
      'setSrv.thState': 'Health',
      'setSrv.thTools': 'Tools',
      'setSrv.thLast': 'Last request',
      'setSrv.thAct': 'Actions',
      'setSrv.toolsTip': 'tool count comes from a probe of this server',
      'setSrv.lastTip': 'from the last 100 calls',
      'setSrv.emptyTitle': 'This set has no servers',
      'setSrv.emptyText': 'Servers live in the host registry; attach one to give this set tools to serve.',
      'tail.thTime': 'Time',
      'tail.thServer': 'Server',
      'tail.thTool': 'Tool',
      'tail.thMs': 'ms',
      'tail.thStatus': 'Status',
      'tail.empty': 'No calls yet',
      'calls.search': 'Search calls',
      'calls.ph': 'tool or client',
      'calls.onlyErr': 'errors only',
      'calls.thClient': 'Client',
      'calls.unavailable': 'call metrics are unavailable: the /api/calls route is not up yet',
      'calls.of': '{n} of {m}',
      'calls.noMatch': 'nothing found — loosen the filter',
      'calls.bad': 'error',
      'calls.ok': 'ok',
      'calls.emptyText': 'Requests appear here as soon as the set serves.',
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
      'stats.requests': 'Requests',
      'stats.ok': 'ok',
      'stats.errors': 'errors',
      'stats.meta': '{calls} {callsW} · {errors} errors · p50 {p50} · p95 {p95}',
      'stats.topTools': 'Top tools',
      'stats.byServer': 'Top servers',
      'stats.bySet': 'By set',
      'stats.byClient': 'Top clients',
      'stats.thTool': 'Tool',
      'stats.thServer': 'Server',
      'stats.thSet': 'Set',
      'stats.thClient': 'Client',
      'stats.thCalls': 'Calls',
      'stats.thErr': 'Err',
      'stats.thP50': 'p50',
      'stats.thP95': 'p95',
      'stats.thShare': 'Share',
      'stats.noData': 'no data in this period',
      'stats.noPerSet': 'No per-set data',
      'stats.noSets': 'per-set traffic appears when every set is selected',
      'stats.noClients': 'no client data in this period',
      'stats.emptyStrong': 'No requests in this period',
      'stats.emptyFilters': 'the filters narrowed the sample to zero — reset them or widen the period',
      'stats.emptyPeriod': 'no calls in the period “{p}”',
      'stats.emptyPeriod2': 'This set recorded no requests in the selected period.',
      'stats.resetFilters': 'Reset filters',
      'stats.unreach': 'no response: {list}',
      'stats.fail': 'the stats request failed: {msg}',
      'stats.more': '{n} more — narrow the filter',
      'stats.colTip': '{label} · {n} calls',
      'stats.colTipErr': '{label} · {n} calls, {err} errors',
      'side.label': 'Details',
      'side.servers': 'servers',
      'side.env': 'variables',
      'side.log': 'log',
      'side.close': 'Close the details panel',
      'side.registry': 'host-wide registry',
      'side.registryEmpty': 'registry is empty',
      'side.server': 'server',
      'form.sub': 'Changes apply to the host registry and take effect on the next set restart.',
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
      'keys.sub': 'A key authenticates one client. It is shown once, right after it is created.',
      'keys.newLabel': 'New key label',
      'keys.noteFor': 'Note for key {id}',
      'keys.notePh': 'note',
      'keys.note': 'Only whoever issued a token knows it: the panel shows the first 8 characters. Revoking a key takes effect without restarting the set.',
      'keys.issue': 'issue a key',
      'keys.once': 'Copy this key now — it is not shown again',
      'keys.copy': 'Copy key',
      'keys.revoke': 'revoke',
      'keys.revoked': 'revoked {date}',
      'keys.active': 'active',
      'keys.restartWarn': 'the note is saved, but the set did not restart: {err}',
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
      'unit.ms': '{n} ms',
      'schema.line': '{bytes} · {tokens} {tok}',
      'srv.edit': 'edit',
      'srv.check': 'check',
      'srv.probe': 'probe',
      'srv.notProbed': 'not probed',
      'srv.notInSet': 'not in this set',
      'srv.setOff': 'set is not running',
      'srv.unchecked': 'not checked',
      'srv.noResponse': 'no response',
      'srv.healthy': 'Healthy',
      'srv.failed': 'Failed',
      'srv.probeFail': 'probe failed',
      'srv.notFound': 'server {name} is not in the registry',
      'err.offline': 'no connection to the daemon',
      'err.http': 'HTTP {status} · {path}',
      'add.nameRequired': 'a name is required'
    },
    ru: {
      'title.panel': 'mcprig — панель',
      'title.set': 'mcprig — набор {name}',
      'a11y.skip': 'Перейти к содержимому',
      'top.details': 'Детали',
      'top.lang': 'Язык',
      'top.theme': 'Цветовая схема',
      'top.updatedLabel': 'обновлено',
      'top.runningW': 'запущено',
      'rail.title': 'Наборы',
      'rail.open': 'Открыть список наборов',
      'rail.collapse': 'Свернуть список наборов',
      'rail.expand': 'Развернуть список наборов',
      'rail.empty': 'наборов нет',
      'rail.add': 'Новый набор',
      'rail.name': 'имя',
      'rail.port': 'порт',
      'rail.create': 'создать',
      'rail.item': '{name} · порт {port} · {state}',
      'common.cancel': 'отмена',
      'common.save': 'сохранить',
      'common.close': 'закрыть',
      'common.done': 'готово',
      'empty.title': 'Наборов пока нет',
      'empty.p': 'Набор — это одна точка входа MCP со своим портом, серверами и ключами.',
      'empty.add': 'Создать первый набор',
      'head.endpoint': 'Адрес',
      'head.copy': 'копировать',
      'head.copyTip': 'скопировать MCP-адрес набора',
      'head.copied': 'скопировано',
      'head.copyFail': 'не удалось',
      'head.key': 'Ключ',
      'head.keyTip': 'показывается только префикс',
      'head.keyOpen': 'набор открыт',
      'head.copyKey': 'Скопировать ключ',
      'head.noKey': 'ключа нет',
      'head.schemaTip': 'сколько контекста стоит перечень инструментов набора',
      'head.servers': 'Серверов',
      'head.tools': 'Инструментов',
      'head.keys': 'Ключи',
      'head.start': 'Запустить',
      'head.restart': 'Перезапустить',
      'head.stop': 'Остановить',
      'head.stopped': 'набор остановлен',
      'head.exited': 'процесс завершился (code {code})',
      'head.exitCode': 'код выхода {code}',
      'head.restarts': 'автоперезапусков: {n}',
      'tabs.overview': 'Обзор',
      'tabs.calls': 'Вызовы',
      'tabs.stats': 'Статистика',
      'kpi.calls': 'вызовов · 7 дней',
      'kpi.errRate': 'доля ошибок',
      'kpi.p50': 'задержка p50',
      'kpi.p95': 'задержка p95',
      'setSrv.title': 'Серверы',
      'setSrv.add': 'Добавить сервер',
      'setSrv.note': 'здоровых {h} · со сбоем {f} · без проверки {u}',
      'setSrv.checking': 'проверяем серверы: {n}…',
      'setSrv.thServer': 'Сервер',
      'setSrv.thType': 'Тип',
      'setSrv.thState': 'Здоровье',
      'setSrv.thTools': 'Инструментов',
      'setSrv.thLast': 'Последний вызов',
      'setSrv.thAct': 'Действия',
      'setSrv.toolsTip': 'число инструментов — из проверки этого сервера',
      'setSrv.lastTip': 'по последним 100 вызовам',
      'setSrv.emptyTitle': 'В наборе нет серверов',
      'setSrv.emptyText': 'Серверы живут в реестре хоста; подключите один, чтобы у набора появились инструменты.',
      'tail.thTime': 'Время',
      'tail.thServer': 'Сервер',
      'tail.thTool': 'Инструмент',
      'tail.thMs': 'мс',
      'tail.thStatus': 'Статус',
      'tail.empty': 'вызовов пока нет',
      'calls.search': 'Поиск вызовов',
      'calls.ph': 'инструмент или клиент',
      'calls.onlyErr': 'только ошибки',
      'calls.thClient': 'Клиент',
      'calls.unavailable': 'метрики вызовов недоступны: маршрут /api/calls ещё не поднят',
      'calls.of': '{n} из {m}',
      'calls.noMatch': 'ничего не найдено — ослабьте фильтр',
      'calls.bad': 'ошибка',
      'calls.ok': 'ок',
      'calls.emptyText': 'Запросы появятся здесь, как только набор начнёт их принимать.',
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
      'stats.requests': 'Запросы',
      'stats.ok': 'ок',
      'stats.errors': 'ошибки',
      'stats.meta': '{calls} {callsW} · ошибок {errors} · p50 {p50} · p95 {p95}',
      'stats.topTools': 'Топ инструментов',
      'stats.byServer': 'Топ серверов',
      'stats.bySet': 'По наборам',
      'stats.byClient': 'Топ клиентов',
      'stats.thTool': 'Инструмент',
      'stats.thServer': 'Сервер',
      'stats.thSet': 'Набор',
      'stats.thClient': 'Клиент',
      'stats.thCalls': 'Вызовов',
      'stats.thErr': 'Ошибок',
      'stats.thP50': 'p50',
      'stats.thP95': 'p95',
      'stats.thShare': 'Доля',
      'stats.noData': 'в этом периоде данных нет',
      'stats.noPerSet': 'Нет данных по наборам',
      'stats.noSets': 'разбивка по наборам видна при выборе всех наборов',
      'stats.noClients': 'в этом периоде нет данных по клиентам',
      'stats.emptyStrong': 'В этом периоде запросов нет',
      'stats.emptyFilters': 'фильтры сузили выборку до нуля — сбросьте их или расширьте период',
      'stats.emptyPeriod': 'в периоде «{p}» вызовов нет',
      'stats.emptyPeriod2': 'В выбранном периоде у набора нет запросов.',
      'stats.resetFilters': 'Сбросить фильтры',
      'stats.unreach': 'нет ответа: {list}',
      'stats.fail': 'запрос статистики не удался: {msg}',
      'stats.more': 'ещё {n} — сузьте фильтр',
      'stats.colTip': '{label} · вызовов {n}',
      'stats.colTipErr': '{label} · вызовов {n}, ошибок {err}',
      'side.label': 'Детали',
      'side.servers': 'серверы',
      'side.env': 'переменные',
      'side.log': 'лог',
      'side.close': 'Закрыть панель деталей',
      'side.registry': 'общий реестр хоста',
      'side.registryEmpty': 'реестр пуст',
      'side.server': 'сервер',
      'form.sub': 'Изменения попадают в реестр хоста и действуют после перезапуска набора.',
      'form.description': 'описание',
      'form.transport': 'транспорт',
      'form.stdio': 'stdio — локальный процесс',
      'form.http': 'http — удалённый endpoint',
      'form.command': 'команда · один аргумент на строку',
      'form.cwd': 'рабочий каталог',
      'form.headers': 'заголовки · K: V',
      'form.env': 'env · KEY=value',
      'form.envAllow': 'envAllow · по одному на строку',
      'form.tags': 'теги · через запятую',
      'form.enabled': 'включён',
      'form.sandbox': 'песочница',
      'form.sandboxHint': 'mode bwrap | none · onUnavailable deny | warn | allow · network none | allow · пусто — значение по умолчанию',
      'form.unset': 'не задано',
      'form.ro': 'ro · по одному на строку',
      'form.rw': 'rw · по одному на строку',
      'form.delete': 'удалить сервер',
      'form.editing': 'сервер · {name}',
      'form.new': 'новый сервер',
      'form.create': 'создать',
      'form.deleteConfirm': 'Удалить сервер {name} из реестра хоста? Он исчезнет из наборов, где используется.',
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
      'keys.sub': 'Ключ аутентифицирует одного клиента. Он показывается один раз — сразу после создания.',
      'keys.newLabel': 'Подпись нового ключа',
      'keys.noteFor': 'Подпись для ключа {id}',
      'keys.notePh': 'подпись',
      'keys.note': 'Токен знает только тот, кто его выпустил: панель показывает первые 8 символов. Отзыв ключа действует без перезапуска набора.',
      'keys.issue': 'выпустить ключ',
      'keys.once': 'Скопируйте ключ сейчас — он больше не покажется',
      'keys.copy': 'Скопировать ключ',
      'keys.revoke': 'отозвать',
      'keys.revoked': 'отозван {date}',
      'keys.active': 'активен',
      'keys.restartWarn': 'подпись сохранена, но набор не перезапустился: {err}',
      'status.running': 'работает',
      'status.failed': 'сбой',
      'status.stopped': 'остановлен',
      'iso.okTip': 'песочница bwrap применена',
      'iso.none': 'без изоляции',
      'iso.rejected': 'конфиг отвергнут',
      'iso.rejectedTip': 'конфиг отвергнут: сервер не запустится',
      'unit.b': 'Б',
      'unit.kb': 'КБ',
      'unit.tokens': 'ток',
      'unit.tools': 'инструментов',
      'unit.h': 'ч',
      'unit.m': 'м',
      'unit.ms': '{n} мс',
      'schema.line': '{bytes} · {tokens} {tok}',
      'srv.edit': 'изменить',
      'srv.check': 'проверить',
      'srv.probe': 'проба',
      'srv.notProbed': 'без пробы',
      'srv.notInSet': 'не в наборе',
      'srv.setOff': 'набор не запущен',
      'srv.unchecked': 'не проверено',
      'srv.noResponse': 'нет ответа',
      'srv.healthy': 'Здоров',
      'srv.failed': 'Сбой',
      'srv.probeFail': 'проба не удалась',
      'srv.notFound': 'сервер {name} не найден в реестре',
      'err.offline': 'нет связи с демоном',
      'err.http': 'HTTP {status} · {path}',
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
    call: { en: ['call', 'calls'], ru: ['вызов', 'вызова', 'вызовов'] },
    tool: { en: ['tool', 'tools'], ru: ['инструмент', 'инструмента', 'инструментов'] },
    client: { en: ['client', 'clients'], ru: ['клиент', 'клиента', 'клиентов'] }
  };

  function word(n, kind) {
    // неизвестный род — не повод ронять рендер: отдаём само слово
    if (!PLURAL[kind]) { return kind; }
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
    health: null, calls: null, kpi: null, env: [], logKey: '', errTimer: 0, busy: false, boot: !!linkId,
    // probe — доказательства строки «Проверить»: они свежее кэша health (15 с)
    probeTools: {}, probeHealth: {}, healthBusy: false,
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

  // проценты — Intl: сам ставит "4.8%" в английском и "4,8 %" в русском
  function pct(fail, all) {
    if (!all) { return '—'; }
    return numFmt({ style: 'percent', minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(fail / all);
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
      renderChrome();
      renderRail();
      renderServers(st.health);
      fillFilters();
      var ids = st.sets.map(function (s) { return s.id; });
      if (ids.indexOf(st.sel) === -1) { select(ids[0] || null); }
      else if (st.boot) { st.boot = false; select(st.sel, true); }
      else { renderHead(); renderSetServers(); }
    }).catch(function (e) { fail(e.message, e); });
  }

  // Шапка метрик не несёт: счётчики хоста живут в статусбаре, как в макете.
  function renderChrome() {
    var running = st.sets.filter(function (s) { return s.runtime && s.runtime.running; }).length;
    var none = st.sets.length === 0;
    $('status-counts').textContent = [
      fmtN(st.sets.length) + ' ' + word(st.sets.length, 'set'),
      fmtN(st.servers.length) + ' ' + word(st.servers.length, 'server'),
      fmtN(running) + ' ' + t('top.runningW')
    ].join(' · ');
    $('f-updated').textContent = clock();
    $('rail-count').textContent = fmtN(st.sets.length);
    show($('empty-state'), none);
    show($('rail-empty'), none);
    show($('phead'), !none);
    show($('tabs'), !none);
    applyTab(none ? null : st.tab);
    // адрес и крошки повторяют открытое; наборы приходят и без выбора
    var set = current();
    $('crumb-set').textContent = set ? (set.name || set.id) : '—';
    $('endpoint').textContent = set
      ? 'http://' + (location.hostname || '127.0.0.1') + ':' + set.port + '/mcp' : '—';
    if (none) { renderKpis(null); renderSetServers(); }
  }

  // Рейл: личность набора и состояние. Цифр трафика здесь нет — они в наборе.
  function renderRail() {
    sync($('rail-sets'), st.sets, function (s) { return s.id; },
      function () {
        var b = el('button', 'set-item');
        b.type = 'button';
        var body = b.appendChild(el('span', 'set-body'));
        body.appendChild(el('span', 'set-name'));
        body.appendChild(el('span', 'set-sub'));
        var state = b.appendChild(el('span', 'state'));
        state.appendChild(el('span', 'dot'));
        return b;
      },
      function (b, s) {
        var state = setState(s);
        var name = s.name || s.id;
        var on = s.id === st.sel;
        b.dataset.id = s.id;
        if (on) { b.setAttribute('aria-current', 'true'); } else { b.removeAttribute('aria-current'); }
        b.querySelector('.set-name').textContent = name;
        b.querySelector('.set-sub').textContent = ':' + s.port + ' · ' + state.text;
        b.querySelector('.state').className = 'state' + (state.cls === 'ok' ? ' state-running'
          : state.cls === 'err' ? ' state-failed' : ' state-stopped');
        b.setAttribute('aria-label', t('rail.item', { name: name, port: s.port, state: state.text }));
      });
  }

  function healthy(h) { return !!(h && h.healthy === true && !h.unknown); }

  // Три честных состояния и «не проверено»: пустое здоровье — не «ок».
  function badgeState(h, running) {
    if (!running) { return { cls: 'badge-unknown', text: t('srv.setOff'), hollow: true }; }
    if (!h) { return { cls: 'badge-unknown', text: t('srv.notProbed'), hollow: true }; }
    if (h.healthy === false) { return { cls: 'badge-err', text: t('srv.failed') }; }
    if (h.unknown) { return { cls: 'badge-unknown', text: t('srv.unchecked'), hollow: true }; }
    return { cls: 'badge-ok', text: t('srv.healthy') };
  }

  // Последний вызов по серверу — из того же хвоста, что и вкладка «Вызовы».
  function lastByServer() {
    var map = {};
    ((st.calls && st.calls.calls) || []).forEach(function (c) {
      var at = new Date(c.time).getTime();
      if (!c.server || isNaN(at)) { return; }
      if (!map[c.server] || map[c.server] < at) { map[c.server] = at; }
    });
    return map;
  }

  /* ---------- серверы набора ---------- */

  function renderSetServers() {
    var set = current();
    var names = (set && set.servers) || [];
    var running = !!(set && set.runtime && set.runtime.running);
    var health = (st.health && st.health.servers) || [];
    var last = lastByServer();
    var rows = names.map(function (name) {
      return {
        name: name,
        srv: st.servers.filter(function (s) { return s.name === name; })[0] || null,
        health: st.probeHealth[name] || health.filter(function (x) { return x.name === name; })[0] || null
      };
    });
    var ok = rows.filter(function (r) { return healthy(r.health); }).length;
    var bad = rows.filter(function (r) { return r.health && r.health.healthy === false; }).length;
    var un = rows.length - ok - bad;
    $('set-srv-note').textContent = st.healthBusy && running
      ? t('setSrv.checking', { n: fmtN(rows.length), serversW: word(rows.length, 'server') })
      : running ? t('setSrv.note', { h: fmtN(ok), f: fmtN(bad), u: fmtN(un) })
        : t('srv.setOff');
    show($('set-srv-empty'), rows.length === 0);
    sync($('set-srv-body'), rows, function (r) { return r.name; },
      function () {
        var tr = el('tr');
        var nm = tr.appendChild(el('td', 'cell-name'));
        nm.dataset.label = t('setSrv.thServer');
        nm.appendChild(el('span', 'sv-name'));
        nm.appendChild(el('span', 'cell-sub'));
        var ty = tr.appendChild(el('td'));
        ty.dataset.label = t('setSrv.thType');
        ty.appendChild(el('span', 'chip'));
        var stc = tr.appendChild(el('td'));
        stc.dataset.label = t('setSrv.thState');
        stc.appendChild(el('span', 'badge badge-unknown'));
        var tools = tr.appendChild(el('td', 'num'));
        tools.dataset.label = t('setSrv.thTools');
        tools.title = t('setSrv.toolsTip');
        var when = tr.appendChild(el('td', 'cell-time hide-sm'));
        when.dataset.label = t('setSrv.thLast');
        when.title = t('setSrv.lastTip');
        var act = tr.appendChild(el('td', 'cell-actions'));
        var check = act.appendChild(el('button', 'btn btn-xs'));
        check.type = 'button';
        check.dataset.check = '';
        var edit = act.appendChild(el('button', 'btn btn-xs'));
        edit.type = 'button';
        edit.dataset.edit = '';
        return tr;
      },
      function (tr, row) {
        var h = row.health;
        tr.dataset.name = row.name;
        tr.querySelector('.sv-name').textContent = row.name;
        tr.children[0].title = (h && h.error) || row.name;
        var iso = isoOf(h && h.isolation);
        var err = (h && h.healthy === false && h.error) || '';
        var sub = tr.querySelector('.cell-sub');
        // вторая строка: причина отказа (текст демона, не перевод) → изоляция → описание
        sub.textContent = err || (iso ? iso.label : '') || (row.srv && row.srv.description) || '';
        sub.hidden = !sub.textContent;
        sub.title = err || (iso ? iso.title : '') || '';
        sub.className = 'cell-sub' + (err || (iso && iso.cls !== 'iso-ok') ? ' cell-iso' : '');
        var srv = row.srv;
        tr.querySelector('.chip').textContent =
          (srv && srv.transport) || (srv && srv.command ? 'stdio' : srv && srv.url ? 'http' : '—');
        var badge = tr.querySelector('.badge');
        if (st.healthBusy && running) {
          badge.className = 'badge badge-unknown';
          badge.replaceChildren(el('span', 'skel'));
        } else {
          var st8 = badgeState(h, running);
          badge.className = 'badge ' + st8.cls;
          badge.replaceChildren(el('span', 'dot' + (st8.hollow ? ' dot-hollow' : '')),
            document.createTextNode(st8.text));
          badge.title = (h && h.error) || '';
        }
        tr.children[3].textContent = st.probeTools[row.name] != null ? fmtN(st.probeTools[row.name]) : '—';
        tr.children[4].textContent = last[row.name] ? hm(last[row.name]) : '—';
        var check = tr.querySelector('[data-check]');
        check.textContent = t('srv.check');
        check.dataset.check = row.name;
        var edit = tr.querySelector('[data-edit]');
        edit.textContent = t('srv.edit');
        edit.dataset.edit = row.name;
      });
  }

  // Одна проба на оба списка: из неё же берётся число инструментов строки.
  function probeServer(name, btn) {
    if (btn) { btn.disabled = true; }
    return send('POST', '/api/servers/' + encodeURIComponent(name) + '/probe').then(function (r) {
      var before = ((st.health && st.health.servers) || []).filter(function (x) { return x.name === name; })[0];
      if (r.ok) {
        st.probeTools[name] = (r.tools || []).length;
        st.probeHealth[name] = { name: name, healthy: true, isolation: before && before.isolation };
      } else {
        st.probeHealth[name] = { name: name, healthy: false, error: r.error || t('srv.noResponse') };
      }
      renderSetServers();
      renderServers(st.health);
      if (!r.ok) { fail(t('srv.probeFail') + ' · ' + (r.error || t('srv.noResponse'))); }
    }).catch(function (e) {
      fail(e.message, e);
    }).then(function () { if (btn) { btn.disabled = false; } });
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
    st.probeTools = {};
    st.probeHealth = {};
    st.healthBusy = false;
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
    applyTab(st.tab);
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
      $('meta-tools').textContent = '—';
      return;
    }
    var state = setState(set);
    var name = set.name || set.id;
    $('set-name').textContent = name;
    $('crumb-set').textContent = name;
    $('set-port').textContent = ':' + set.port;
    $('set-state').className = 'badge ' + (state.cls === 'ok' ? 'badge-ok'
      : state.cls === 'err' ? 'badge-err' : 'badge-warn');
    $('set-state-t').textContent = state.text;
    $('endpoint').textContent = 'http://' + (location.hostname || '127.0.0.1') + ':' + set.port + '/mcp';
    // только префикс: токена в панели нет и быть не может
    $('key').textContent = set.keyPrefix ? set.keyPrefix + '…'
      : (set.open ? t('head.keyOpen') : t('head.noKey'));
    $('keys-count').textContent = fmtN((set.keys || []).length);
    $('meta-servers').textContent = fmtN((set.servers || []).length);
    var schema = (st.calls && st.calls.schema) || null;
    $('meta-tools').textContent = schema ? fmtN(schema.tools) : '—';
    $('schema').textContent = schemaLine(schema);
    var running = !!(set.runtime && set.runtime.running);
    // Stop — главное действие запущенного набора; перезапуск рядом и тише
    $('run').textContent = running ? t('head.stop') : t('head.start');
    $('run').className = running ? 'btn' : 'btn btn-primary';
    show($('restart'), running);
    $('dlg-set').textContent = name;

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
      $('set-error-d').textContent = lines.join(' · ');
    }
  }

  // Полоса KPI — срез самого набора за 7 дней: не счётчики процесса, которые после
  // рестарта начинаются с нуля, и не «сегодня», которое обнуляется в полночь.
  function loadKpi() {
    if (!st.sel) { renderKpis(null); return; }
    var id = st.sel;
    return get('/api/stats?set=' + encodeURIComponent(id) + '&period=7d&bucket=day')
      .then(function (data) {
        if (id !== st.sel) { return; }
        st.kpi = data || null;
        renderKpis(st.kpi);
      })
      .catch(function (e) { if (id === st.sel) { renderKpis(null); } fail(e.message, e); });
  }

  function renderKpis(data) {
    var tot = data && data.totals;
    $('k-calls').textContent = tot ? fmtN(tot.calls) : '—';
    $('k-err').textContent = tot && tot.calls ? pct(tot.failures, tot.calls) : '—';
    $('k-p50').textContent = tot ? ms(tot.p50ms) : '—';
    $('k-p95').textContent = tot ? ms(tot.p95ms) : '—';
    // прочерк бледнее цифры: пустая полоса не притворяется нулём
    [].forEach.call(document.querySelectorAll('.kpi'), function (k) {
      var n = k.querySelector('.kpi-num');
      n.classList.toggle('is-muted', !tot || n.textContent === '—');
    });
  }

  function loadHealth() {
    if (!st.sel) { return; }
    var id = st.sel;
    // скелетон только когда здоровья ещё нет: тикер каждые 5 с не должен мигать
    if (!st.health) {
      st.healthBusy = true;
      renderSetServers();
    }
    return get('/api/sets/' + id + '/servers').then(function (data) {
      if (id !== st.sel) { return; }
      st.health = data;
      st.healthBusy = false;
      renderSetServers();
      renderServers(data);
    }).catch(function (e) {
      st.healthBusy = false;
      renderSetServers();
      fail(e.message, e);
    });
  }

  /* ---------- реестр хоста (дровер «Детали») ---------- */

  function renderServers(health) {
    var set = current();
    var inSet = (set && set.servers) || [];
    // список — общий реестр хоста, а не состав набора: новый сервер виден сразу,
    // а принадлежность к набору помечена в строке
    var rows = st.servers.map(function (s) {
      var h = health && health.servers
        ? health.servers.filter(function (x) { return x.name === s.name; })[0] || null : null;
      return { name: s.name, srv: s, health: h, mine: inSet.indexOf(s.name) >= 0 };
    });
    $('srv-count').textContent = fmtN(rows.length);
    show($('servers-empty'), rows.length === 0);
    sync($('servers'), rows, function (r) { return r.name; },
      function () {
        var tr = el('tr');
        var nm = tr.appendChild(el('td', 'cell-name'));
        nm.dataset.label = t('setSrv.thServer');
        nm.appendChild(el('span', 'sv-name'));
        nm.appendChild(el('span', 'cell-sub'));
        var ty = tr.appendChild(el('td'));
        ty.dataset.label = t('setSrv.thType');
        ty.appendChild(el('span', 'chip'));
        var act = tr.appendChild(el('td', 'cell-actions'));
        var probe = act.appendChild(el('button', 'btn btn-xs'));
        probe.type = 'button';
        probe.dataset.probe = '';
        var edit = act.appendChild(el('button', 'btn btn-xs'));
        edit.type = 'button';
        edit.dataset.regedit = '';
        return tr;
      },
      function (tr, row) {
        var srv = row.srv;
        var h = row.health;
        tr.dataset.name = row.name;
        tr.querySelector('.sv-name').textContent = row.name;
        var iso = isoOf(h && h.isolation);
        var sub = tr.querySelector('.cell-sub');
        // причина ошибки приходит от демона по-русски — её не переводим
        var note = !row.mine ? t('srv.notInSet')
          : h && h.healthy === false ? (h.error || t('srv.noResponse'))
            : h && h.healthy === true && h.unknown ? t('srv.unchecked')
              : iso ? iso.label : (srv.description || '');
        sub.textContent = note || '';
        sub.hidden = !note;
        sub.title = (h && h.error) || '';
        sub.className = 'cell-sub' + (h && h.healthy === false ? ' cell-iso' : '');
        tr.querySelector('.chip').textContent =
          srv.transport || (srv.command ? 'stdio' : srv.url ? 'http' : '—');
        var probe = tr.querySelector('[data-probe]');
        probe.textContent = t('srv.probe');
        probe.dataset.probe = row.name;
        var edit = tr.querySelector('[data-regedit]');
        edit.textContent = t('srv.edit');
        edit.dataset.regedit = row.name;
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
    var dlg = $('srv-dlg');
    if (!dlg.open) { return; }
    dlg.close();
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
    var dlg = $('srv-dlg');
    if (typeof dlg.showModal === 'function' && !dlg.open) { dlg.showModal(); }
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
      loadKpi();
      renderCalls(st.calls);
      renderHead();
      renderSetServers();
    }).catch(function (e) { fail(e.message, e); });
  }

  /* ---------- вкладка «Вызовы» ---------- */

  function renderCalls(data) {
    var box = $('calls-body');
    var note = $('calls-note');

    if (!data) {
      box.replaceChildren();
      $('calls-count').textContent = cnt(0, 'call');
      show(note, true);
      note.querySelector('.empty-text').textContent = t('calls.unavailable');
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
      var time = tr.appendChild(el('td', 'cell-time', clock(c.time)));
      time.dataset.label = t('tail.thTime');
      var srv = tr.appendChild(el('td', '', c.server || '—'));
      srv.dataset.label = t('tail.thServer');
      var tool = tr.appendChild(el('td', 'cell-name', c.tool || '—'));
      tool.dataset.label = t('tail.thTool');
      var cl = tr.appendChild(el('td', '', c.client || '—'));
      cl.dataset.label = t('calls.thClient');
      var dur = tr.appendChild(el('td', 'num' + (c.ms > 1000 ? ' slow' : ''), fmtN(c.ms)));
      dur.dataset.label = t('tail.thMs');
      var stc = tr.appendChild(el('td'));
      stc.dataset.label = t('tail.thStatus');
      var badge = stc.appendChild(el('span', 'badge ' + (c.failed ? 'badge-err' : 'badge-ok')));
      badge.appendChild(el('span', 'dot' + (c.failed ? '' : '')));
      badge.appendChild(document.createTextNode(c.failed ? t('calls.bad') : t('calls.ok')));
      badge.title = c.error || '';
      frag.appendChild(tr);
    });
    box.replaceChildren(frag);

    $('calls-count').textContent = rows.length === all.length
      ? cnt(all.length, 'call') : t('calls.of', { n: fmtN(rows.length), m: fmtN(all.length) });
    show(note, rows.length === 0);
    note.querySelector('.empty-title').textContent = all.length ? t('calls.noMatch') : t('tail.empty');
    note.querySelector('.empty-text').textContent = t('calls.emptyText');
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
        var li = el('li');
        li.appendChild(el('code'));
        li.appendChild(el('span', 'v-state'));
        return li;
      },
      function (li, v) {
        var code = li.querySelector('code');
        code.textContent = v.name;
        code.title = v.name;
        li.querySelector('.v-state').textContent = (v.set ? t('env.set') : t('env.unset')) +
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

  function cnt(n, w) { return fmtN(n) + ' ' + word(n, w); }

  function renderStats(d) {
    var tot = (d && d.totals) || { calls: 0 };
    var empty = !tot.calls;
    show($('stats-empty'), empty);
    show($('s-chart-block'), !empty);
    show($('s-grid'), !empty);

    var unreach = (d && d.unreachable) || [];
    show($('stats-unreach'), unreach.length > 0);
    $('stats-unreach').textContent = unreach.length ? t('stats.unreach', { list: unreach.join(', ') }) : '';

    if (empty) {
      var narrowed = !!(st.st.server || st.st.tool || st.st.client);
      $('stats-empty-why').textContent = narrowed
        ? t('stats.emptyFilters') : t('stats.emptyPeriod', { p: periodName(st.st.period) });
      $('s-meta').textContent = '';
      $('s-range').textContent = '';
      return;
    }

    var meta = t('stats.meta', {
      calls: fmtN(tot.calls), callsW: word(tot.calls, 'call'),
      errors: fmtN(tot.failures), p50: ms(tot.p50ms), p95: ms(tot.p95ms)
    });
    $('s-meta').textContent = d.approximate ? meta + ' ≈' : meta;
    if (d.approximate) { $('s-meta').title = t('stats.approxTip'); } else { $('s-meta').removeAttribute('title'); }

    renderSeries(d.series || []);
    $('s-tools-count').textContent = cnt((d.tools || []).length, 'tool');
    $('s-servers-count').textContent = cnt((d.servers || []).length, 'server');
    $('s-clients-count').textContent = cnt((d.clients || []).length, 'client');
    $('s-byset-count').textContent = Array.isArray(d.bySet) ? cnt(d.bySet.length, 'set') : '';

    renderBreakdown($('tools-body'), d.tools, 'tool', t('stats.thTool'), 'server', $('tools-empty'), 11);
    renderBreakdown($('byserver-body'), d.servers, 'server', t('stats.thServer'), '', $('byserver-empty'), 11);
    renderClients(d.clients || []);
    renderSets(d.bySet);
  }

  // group — сортировка готовых строк бэкенда по вызовам, без собственной агрегации.
  function renderBreakdown(tbody, rows, field, label, subField, emptyNode, limit) {
    var list = (rows || []).slice().sort(function (a, b) { return (b.calls || 0) - (a.calls || 0); });
    show(emptyNode, list.length === 0);
    var frag = document.createDocumentFragment();
    list.slice(0, limit || list.length).forEach(function (g) {
      var tr = el('tr');
      var name = tr.appendChild(el('td', 'cell-name'));
      name.dataset.label = label;
      name.appendChild(el('span', 'bd-name', g[field] || '—'));
      var sub = name.appendChild(el('span', 'cell-sub', subField ? (g[subField] || '') : ''));
      sub.hidden = !sub.textContent;
      addNum(tr, g.calls, t('stats.thCalls'));
      var f = addNum(tr, pct(g.failures, g.calls), t('stats.thErr'));
      if (g.failures) { f.classList.add('bad'); }
      addNum(tr, ms(g.p50ms), t('stats.thP50'));
      addNum(tr, ms(g.p95ms), t('stats.thP95'));
      frag.appendChild(tr);
    });
    if (limit && list.length > limit) {
      var rest = frag.appendChild(el('tr'));
      var td = rest.appendChild(el('td', 'block-meta', t('stats.more', { n: fmtN(list.length - limit) })));
      td.colSpan = 5;
    }
    tbody.replaceChildren(frag);
  }

  function addNum(tr, text, label) {
    var td = tr.appendChild(el('td', 'num', text));
    td.dataset.label = label;
    return td;
  }

  // By set: доля набора — полосой, как в макете.
  function renderSets(bySet) {
    var empty = $('byset-empty');
    if (!Array.isArray(bySet)) {
      show(empty, true);
      empty.querySelector('.empty-title').textContent = t('stats.noSets');
      $('byset-body').replaceChildren();
      return;
    }
    var total = bySet.reduce(function (s, r) { return s + (r.calls || 0); }, 0) || 1;
    var list = bySet.slice().sort(function (a, b) { return (b.calls || 0) - (a.calls || 0); });
    show(empty, list.length === 0);
    var frag = document.createDocumentFragment();
    list.forEach(function (g) {
      var tr = el('tr');
      var name = tr.appendChild(el('td', 'cell-name', setName(g.set)));
      name.dataset.label = t('stats.thSet');
      addNum(tr, fmtN(g.calls), t('stats.thCalls'));
      var shareTd = tr.appendChild(el('td'));
      shareTd.dataset.label = t('stats.thShare');
      var share = Math.round((g.calls || 0) / total * 100);
      var bar = shareTd.appendChild(el('div', 'share'));
      var track = bar.appendChild(el('div', 'share-bar'));
      var fill = track.appendChild(el('i'));
      fill.style.width = Math.max(2, share) + '%';
      bar.appendChild(el('span', 'share-n', share + ' %'));
      var f = addNum(tr, pct(g.failures, g.calls), t('stats.thErr'));
      if (g.failures) { f.classList.add('bad'); }
      frag.appendChild(tr);
    });
    $('byset-body').replaceChildren(frag);
  }

  // Top clients: бэкенд отдаёт client первым полем, но строки бывают пустыми
  function renderClients(rows) {
    var list = (rows || []).filter(function (r) { return r.client; })
      .sort(function (a, b) { return (b.calls || 0) - (a.calls || 0); });
    show($('byclient-empty'), list.length === 0);
    var frag = document.createDocumentFragment();
    list.slice(0, 11).forEach(function (g) {
      var tr = el('tr');
      var name = tr.appendChild(el('td', 'cell-name', g.client));
      name.dataset.label = t('stats.thClient');
      addNum(tr, g.calls, t('stats.thCalls'));
      var f = addNum(tr, pct(g.failures, g.calls), t('stats.thErr'));
      if (g.failures) { f.classList.add('bad'); }
      addNum(tr, ms(g.p50ms), t('stats.thP50'));
      addNum(tr, ms(g.p95ms), t('stats.thP95'));
      frag.appendChild(tr);
    });
    $('byclient-body').replaceChildren(frag);
  }

  /* График рисуем сами: SVG с шириной контейнера, столбики ok+err, 5 линий сетки.
     Перерисовка на resize и на открытие вкладки — иначе во скрытой вкладке
     контейнер нулевой ширины. */
  var SVGNS = 'http://www.w3.org/2000/svg';

  function svgEl(tag, attrs) {
    var n = document.createElementNS(SVGNS, tag);
    Object.keys(attrs || {}).forEach(function (k) { n.setAttribute(k, attrs[k]); });
    return n;
  }

  function niceMax(v) {
    if (!(v > 0)) { return 1; }
    var pow = Math.pow(10, Math.floor(Math.log(v) / Math.LN10));
    var f = v / pow;
    var m = f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10;
    return m * pow;
  }

  function renderSeries(rows) {
    var box = $('chart-wrap');
    var svg = $('chart');
    var hour = st.st.period === 'today';
    var monthly = st.st.period === 'archive';
    var label = hour ? function (iso) { return hm(new Date(iso).getTime()); }
      : monthly ? mon : day;
    var has = (rows || []).some(function (r) { return r.calls > 0; });
    show($('activity-empty'), !has);
    show(box, has);
    $('activity-empty-text').textContent = (st.st.server || st.st.tool || st.st.client)
      ? t('stats.emptyFilters') : t('stats.emptyPeriod2');
    if (!has) { svg.replaceChildren(); $('s-range').textContent = ''; return; }

    var W = Math.max(280, box.clientWidth || 640);
    var H = W < 560 ? 190 : 236;
    var ml = 46, mr = 6, mt = 12, mb = 26;
    var iw = W - ml - mr, ih = H - mt - mb;
    var max = niceMax(Math.max.apply(null, rows.map(function (r) { return r.calls || 0; })));
    var band = iw / rows.length;
    var barW = Math.min(38, band * 0.62);
    var base = mt + ih;
    var step = max / 4;
    var frag = document.createDocumentFragment();

    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    svg.setAttribute('aria-label', t('stats.requests'));

    for (var i = 0; i <= 4; i++) {
      var y = mt + ih * i / 4;
      frag.appendChild(svgEl('line', {
        x1: ml, y1: y.toFixed(1), x2: W - mr, y2: y.toFixed(1),
        stroke: 'var(--chart-grid)', 'stroke-width': 1
      }));
      var lab = svgEl('text', {
        x: ml - 8, y: (y + 3.5).toFixed(1), 'text-anchor': 'end',
        fill: 'var(--text-mute)', 'font-size': 10, 'font-family': 'var(--font-mono)'
      });
      var v = max - max * i / 4;
      // шаг 0.25 → два знака: иначе подписи «0.25» и «0.3» врут
      var d = step < 0.5 ? 2 : step < 1 ? 1 : 0;
      lab.textContent = fmtN(d ? Number(v.toFixed(d)) : Math.round(v));
      frag.appendChild(lab);
    }

    rows.forEach(function (r, k) {
      var n = r.calls || 0;
      var err = Math.min(r.failures || 0, n);
      var hErr = err / max * ih, hOk = (n - err) / max * ih;
      var x = ml + band * k + (band - barW) / 2;
      var g = svgEl('g', {});
      if (hOk > 0) {
        g.appendChild(svgEl('rect', {
          x: x.toFixed(1), y: (base - hOk).toFixed(1), width: barW.toFixed(1),
          height: Math.max(1, hOk).toFixed(1), rx: 1.5, fill: 'var(--chart-ok)'
        }));
      }
      if (hErr > 0) {
        g.appendChild(svgEl('rect', {
          x: x.toFixed(1), y: (base - hOk - hErr).toFixed(1), width: barW.toFixed(1),
          height: Math.max(1, hErr).toFixed(1), rx: 1.5, fill: 'var(--chart-err)'
        }));
      }
      var tip = svgEl('title', {});
      tip.textContent = err
        ? t('stats.colTipErr', { label: label(r.t), n: fmtN(n), err: fmtN(err) })
        : t('stats.colTip', { label: label(r.t), n: fmtN(n) });
      g.appendChild(tip);
      frag.appendChild(g);
    });

    var stride = Math.max(1, Math.ceil(rows.length / Math.max(2, Math.floor(iw / 70))));
    rows.forEach(function (r, k) {
      if (k % stride) { return; }
      var lab = svgEl('text', {
        x: (ml + band * k + band / 2).toFixed(1), y: H - 8, 'text-anchor': 'middle',
        fill: 'var(--text-mute)', 'font-size': 10, 'font-family': 'var(--font-mono)'
      });
      lab.textContent = label(r.t);
      frag.appendChild(lab);
    });

    svg.replaceChildren(frag);
    $('s-range').textContent = label(rows[0].t) + ' — ' + label(rows[rows.length - 1].t);
  }

  function applyTab(name) {
    ['overview', 'calls', 'stats'].forEach(function (tab) {
      var btn = $('tabbtn-' + tab);
      var on = tab === name;
      btn.setAttribute('aria-selected', on ? 'true' : 'false');
      btn.tabIndex = on ? 0 : -1;
      show($('tab-' + tab), on);
    });
  }

  /* ---------- вкладки основной области ---------- */

  function setTab(name) {
    st.tab = name;
    applyTab(name);
    // адрес повторяет то, что открыто: у статистики в нём ещё и период
    var hash = name === 'stats' ? '#/stats/' + st.st.period : (st.sel ? '#/set/' + st.sel : '');
    if (hash && location.hash !== hash) { history.replaceState(null, '', hash); }
    if (name === 'stats') {
      if (!st.st.data) { loadStats(); }
      // ширина контейнера известна только после показа вкладки
      renderSeries((st.st.data && st.st.data.series) || []);
    }
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
      var btn = document.querySelector('.drawer-seg .segbtn[data-side="' + p + '"]');
      var on = p === name;
      btn.setAttribute('aria-selected', on ? 'true' : 'false');
      btn.tabIndex = on ? 0 : -1;
      show(document.querySelector('.pane[data-pane="' + p + '"]'), on);
    });
  }

  function openSide() {
    st.sideReturn = document.activeElement;
    document.body.classList.add('side-open');
    $('side-open').setAttribute('aria-expanded', 'true');
    $('side').setAttribute('aria-hidden', 'false');
    show($('scrim'), true);
    if (window.innerWidth < 1200) {
      var tab = document.querySelector('.drawer-seg .segbtn[aria-selected="true"]') || $('side-close');
      tab.focus();
    }
  }

  function closeSide() {
    var was = document.body.classList.contains('side-open');
    document.body.classList.remove('side-open');
    $('side-open').setAttribute('aria-expanded', 'false');
    $('side').setAttribute('aria-hidden', 'true');
    if (!document.body.classList.contains('rail-open')) { show($('scrim'), false); }
    // фокус не должен оставаться в скрытой панели: возвращаем туда, откуда открыли,
    // а если оттуда некуда (открыли программно) — на саму кнопку «Детали»
    var back = st.sideReturn && document.contains(st.sideReturn) && st.sideReturn.offsetParent !== null
      ? st.sideReturn : ($('side-open').offsetParent !== null ? $('side-open') : null);
    st.sideReturn = null;
    if (was && back) { back.focus(); }
  }

  function openRail() {
    document.body.classList.add('rail-open');
    $('rail-open').setAttribute('aria-expanded', 'true');
    show($('scrim'), true);
    var first = document.querySelector('.set-item') || $('rail-toggle');
    if (first) { first.focus(); }
  }

  function closeRail() {
    var was = document.body.classList.contains('rail-open');
    document.body.classList.remove('rail-open');
    $('rail-open').setAttribute('aria-expanded', 'false');
    if (!document.body.classList.contains('side-open')) { show($('scrim'), false); }
    if (was) { $('rail-open').focus(); }
  }

  function applyRail() {
    var w = window.innerWidth;
    // ниже 1180 рейл отдаём иконкам; на узком экране он живёт в off-canvas
    var auto = w >= 960 && w < 1180;
    var compact = st.railPref ? st.railPref === 'compact' : auto;
    document.documentElement.classList.toggle('rail-compact', compact && w >= 860);
    var btn = $('rail-toggle');
    btn.setAttribute('aria-pressed', String(compact));
    btn.setAttribute('aria-label', compact ? t('rail.expand') : t('rail.collapse'));
    if (w >= 860) { closeRail(); } else { closeSide(); }
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
    show($('new-key-box'), false);
    show($('keys-warn'), false);
    $('key-label').value = '';
    var dlg = $('keys-dlg');
    if (typeof dlg.showModal === 'function') { dlg.showModal(); }
  }

  function renderKeys(set) {
    set = set || {};
    var keys = set.keys || [];
    show($('keys-empty'), keys.length === 0);
    sync($('key-list'), keys, function (k) { return k.id; },
      function () {
        var li = el('li', 'keyitem');
        var head = li.appendChild(el('div'));
        head.appendChild(el('span', 'key-name'));
        head.appendChild(el('span', 'key-sub'));
        var note = li.appendChild(el('div', 'key-note'));
        var input = note.appendChild(el('input', 'input'));
        input.type = 'text';
        input.maxLength = 64;
        input.autocomplete = 'off';
        var save = note.appendChild(el('button', 'btn btn-sm'));
        save.type = 'button';
        save.dataset.noteSave = '';
        var acts = li.appendChild(el('div', 'key-acts'));
        var rev = acts.appendChild(el('button', 'btn btn-xs btn-danger'));
        rev.type = 'button';
        return li;
      },
      function (li, k) {
        var revoked = !!k.revokedAt;
        li.dataset.revoked = String(revoked);
        // имя строки — id ключа: префикс у всех ключей набора один и тот же
        li.querySelector('.key-name').textContent = k.id;
        li.querySelector('.key-sub').textContent = revoked
          ? day(k.createdAt) + ' · ' + t('keys.revoked', { date: day(k.revokedAt) })
          : day(k.createdAt) + ' · ' + t('keys.active');
        var input = li.querySelector('.key-note .input');
        input.value = k.note || '';
        input.placeholder = t('keys.notePh');
        input.disabled = revoked;
        input.dataset.note = k.id;
        input.setAttribute('aria-label', t('keys.noteFor', { id: k.id }));
        var save = li.querySelector('[data-note-save]');
        save.textContent = t('common.save');
        save.dataset.note = k.id;
        save.hidden = revoked || input.value.trim() === (k.note || '');
        var rev = li.querySelector('.key-acts .btn');
        rev.textContent = t('keys.revoke');
        rev.dataset.revoke = k.id;
        rev.hidden = revoked;
      });
  }

  // Подпись ключа — это метка клиента в статистике, поэтому демон перезапускает
  // работающий набор. Неудачу рестарта он отдаёт полем restartError: подпись
  // уже сохранена, и молчать об этом нельзя.
  function saveNote(keyID, input, btn) {
    var set = current();
    if (!set || st.busy) { return; }
    st.busy = true;
    btn.disabled = true;
    show($('keys-warn'), false);
    send('PATCH', '/api/sets/' + encodeURIComponent(set.id) + '/keys/' + encodeURIComponent(keyID),
      { note: input.value.trim() }).then(function (r) {
        if (r && r.restartError) {
          $('keys-warn').textContent = t('keys.restartWarn', { err: r.restartError });
          show($('keys-warn'), true);
        }
        return loadState().then(function () { renderKeys(current() || set); });
      }).catch(function (e) { fail(e.message, e); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  /* ---------- язык ----------
     Переключение перерисовывает всё, что панель собрала сама: статику обходит
     applyStatic, данные — заново теми же render-функциями. */
  var relang = function () {
    applyStatic();
    setTitle();
    renderChrome();
    renderRail();
    renderHead();
    renderKpis(st.kpi);
    renderSetServers();
    renderServers(st.health);
    renderCalls(st.calls);
    renderEnv();
    renderKeys(current() || {});
    fillFilters();
    renderStats(st.st.data);
    applyRail();
    applyThemeMenu();
  };

  /* ---------- привязки ---------- */

  $('rail-sets').addEventListener('click', function (e) {
    var row = e.target.closest('.set-item');
    if (!row) { return; }
    select(row.dataset.id);
    closeRail();
  });

  $('rail-open').addEventListener('click', function () {
    if (document.body.classList.contains('rail-open')) { closeRail(); } else { openRail(); }
  });

  $('rail-toggle').addEventListener('click', function () {
    st.railPref = document.documentElement.classList.contains('rail-compact') ? 'full' : 'compact';
    try { localStorage.setItem('mcprig.rail', st.railPref); } catch (err) { /* приватный режим */ }
    applyRail();
  });

  $('scrim').addEventListener('click', function () { closeSide(); closeRail(); });

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

  document.querySelector('.drawer-seg').addEventListener('click', function (e) {
    var btn = e.target.closest('.segbtn');
    if (btn) { setSideTab(btn.dataset.side); }
  });
  document.querySelector('.drawer-seg').addEventListener('keydown', function (e) {
    arrowNav(e, this, function (b) { setSideTab(b.dataset.side); });
  });
  $('s-period').addEventListener('keydown', function (e) {
    arrowNav(e, this, function (b) { setPeriod(b.dataset.period); });
  });

  // Tab не уходит ни в контент под дровером, ни за открытую форму сервера
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      if ($('theme-menu').hidden === false) { closeThemeMenu(true); return; }
      if ($('srv-dlg').open) { closeSrv(); return; }
      if (document.body.classList.contains('side-open')) { closeSide(); return; }
      if (document.body.classList.contains('rail-open')) { closeRail(); }
      return;
    }
    if (e.key !== 'Tab') { return; }
    var root = $('srv-dlg').open ? $('srv-dlg') : null;
    if (!root && !document.body.classList.contains('side-open')) { return; }
    root = root || $('side');
    var f = [].slice.call(root.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'))
      .filter(function (n) { return n.offsetParent !== null; });
    if (!f.length) { return; }
    var first = f[0], last = f[f.length - 1], at = document.activeElement;
    if (e.shiftKey && (at === first || !root.contains(at))) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && at === last) { e.preventDefault(); first.focus(); }
  });

  /* ---------- форма сервера ---------- */

  $('srv-add').addEventListener('click', function () { openSrv(''); });
  $('srvform-close').addEventListener('click', function () { closeSrv(); });
  $('srv-cancel').addEventListener('click', function () { closeSrv(); });
  $('srvform').elements.transport.addEventListener('change', srvWhen);
  $('srvform').addEventListener('submit', saveSrv);
  $('srv-del').addEventListener('click', delSrv);

  $('side-open').addEventListener('click', openSide);
  $('side-close').addEventListener('click', closeSide);

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
  function resetFilters() {
    st.st.set = st.sel || '';
    st.st.server = '';
    st.st.tool = '';
    st.st.client = '';
    fillFilters();
    loadStats();
  }
  $('s-reset').addEventListener('click', resetFilters);
  $('activity-reset').addEventListener('click', resetFilters);
  [].forEach.call(document.querySelectorAll('#s-period .segbtn'), function (btn) {
    btn.addEventListener('click', function () { setPeriod(btn.dataset.period, false); });
  });

  // «/» — к поиску вызовов, как в почтовых клиентах
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

  var resizeTimer = 0;
  window.addEventListener('resize', function () {
    applyRail();
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      if (st.tab === 'stats' && st.st.data) { renderSeries(st.st.data.series || []); }
    }, 120);
  });

  /* ---------- язык ---------- */

  var langSel = $('lang');
  langSel.value = lang;
  langSel.addEventListener('change', function () {
    lang = langSel.value === 'ru' ? 'ru' : 'en';
    try { localStorage.setItem(LANG_KEY, lang); } catch (e) { /* приватный режим */ }
    relang();
  });

  /* ---------- тема ---------- */

  // сам атрибут ставит theme.js до первой отрисовки; меню только меняет его
  function applyThemeMenu() {
    var on = document.documentElement.getAttribute('data-theme') || 'dark';
    [].forEach.call(document.querySelectorAll('[data-theme-opt]'), function (b) {
      b.setAttribute('aria-checked', String(b.dataset.themeOpt === on));
    });
  }

  function closeThemeMenu(back) {
    $('theme-menu').hidden = true;
    $('theme-btn').setAttribute('aria-expanded', 'false');
    if (back) { $('theme-btn').focus(); }
  }

  $('theme-btn').addEventListener('click', function (e) {
    e.stopPropagation();
    var open = $('theme-menu').hidden;
    $('theme-menu').hidden = !open;
    this.setAttribute('aria-expanded', String(open));
    if (open) {
      var cur = document.querySelector('[data-theme-opt][aria-checked="true"]');
      if (cur) { cur.focus(); }
    }
  });

  $('theme-menu').addEventListener('click', function (e) {
    var btn = e.target.closest('[data-theme-opt]');
    if (!btn) { return; }
    var name = btn.dataset.themeOpt;
    document.documentElement.setAttribute('data-theme', name);
    try { localStorage.setItem('mcprig.theme', name); } catch (err) { /* приватный режим */ }
    applyThemeMenu();
    closeThemeMenu(true);
  });

  document.addEventListener('click', function (e) {
    if ($('theme-menu').hidden) { return; }
    if (!$('tmenu').contains(e.target)) { closeThemeMenu(false); }
  });

  /* ---------- копирование ---------- */

  var copyTimer = 0;
  function copyText(text, btn, label) {
    var back = label || btn.textContent;
    var done = function (ok) {
      btn.textContent = ok ? t('head.copied') : t('head.copyFail');
      clearTimeout(copyTimer);
      copyTimer = setTimeout(function () { btn.textContent = back; }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { done(true); }, function () { done(false); });
    } else {
      done(false);
    }
  }

  $('copy').addEventListener('click', function () {
    copyText($('endpoint').textContent, this);
  });

  // токена в панели нет: копировать можно только префикс, и это честно подписано
  $('key-copy').addEventListener('click', function () {
    var text = $('key').textContent || '';
    if (!text || text === '—') { return; }
    copyText(text, this);
  });

  // один и тот же путь для обеих кнопок: what — start|stop|restart
  function runAct(btn, what) {
    var set = current();
    if (!set) { return; }
    act(btn, '/api/sets/' + set.id + '/' + what, function () {
      st.health = null;
      return loadState().then(loadHealth);
    });
  }

  $('run').addEventListener('click', function () {
    var set = current();
    runAct(this, set && set.runtime && set.runtime.running ? 'stop' : 'start');
  });

  $('restart').addEventListener('click', function () { runAct(this, 'restart'); });

  $('log-refresh').addEventListener('click', function () { loadLog(); });
  $('log-wide').addEventListener('click', function () {
    var on = document.body.classList.toggle('log-wide');
    this.setAttribute('aria-pressed', on ? 'true' : 'false');
  });
  $('errbar-close').addEventListener('click', function () { show($('errbar'), false); });

  /* ---------- список серверов набора и реестра ---------- */

  $('set-srv-body').addEventListener('click', function (e) {
    var check = e.target.closest('[data-check]');
    if (check) { probeServer(check.dataset.check, check); return; }
    var edit = e.target.closest('[data-edit]');
    if (edit) { openSrv(edit.dataset.edit); }
  });

  $('servers').addEventListener('click', function (e) {
    var probe = e.target.closest('[data-probe]');
    if (probe) { probeServer(probe.dataset.probe, probe); return; }
    var edit = e.target.closest('[data-regedit]');
    if (edit) { openSrv(edit.dataset.regedit); }
  });

  /* ---------- новый набор ---------- */

  var form = $('addform');
  $('add-toggle').addEventListener('click', function () {
    form.hidden = !form.hidden;
    $('add-err').hidden = true;
    if (!form.hidden) { $('add-name').focus(); }
  });
  $('empty-add').addEventListener('click', function () {
    form.hidden = false;
    $('add-name').focus();
  });
  $('add-cancel').addEventListener('click', function () {
    form.hidden = true;
    $('add-err').hidden = true;
  });
  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var name = $('add-name').value.trim();
    var portRaw = $('add-port').value.trim();
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

  /* ---------- ключи ---------- */

  $('keys').addEventListener('click', openKeys);
  $('keys-close').addEventListener('click', function () { $('keys-dlg').close(); });
  $('keys-done').addEventListener('click', function () { $('keys-dlg').close(); });

  $('new-key-copy').addEventListener('click', function () {
    copyText($('new-key').textContent, this);
  });

  // Выдача ключа подписью не принимает — подпись ставим отдельным PATCH,
  // а ошибку второго рестарта показываем тем же предупреждением.
  $('key-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var set = current();
    if (!set || st.busy) { return; }
    var label = $('key-label').value.trim();
    var btn = this.querySelector('button[type="submit"]');
    st.busy = true;
    btn.disabled = true;
    show($('keys-warn'), false);
    send('POST', '/api/sets/' + encodeURIComponent(set.id) + '/keys', {})
      .then(function (r) {
        $('key-label').value = '';
        $('new-key').textContent = (r && r.token) || '';
        show($('new-key-box'), !!(r && r.token));
        if (r && r.restartError) { fail(t('keys.restartWarn', { err: r.restartError })); }
        var key = r && r.key && r.key.id;
        var note = label && key
          ? send('PATCH', '/api/sets/' + encodeURIComponent(set.id) + '/keys/' + encodeURIComponent(key), { note: label })
          : Promise.resolve(null);
        return note.then(function (signed) {
          if (signed && signed.restartError) {
            $('keys-warn').textContent = t('keys.restartWarn', { err: signed.restartError });
            show($('keys-warn'), true);
          }
          return loadState().then(function () { renderKeys(current() || set); });
        });
      })
      .catch(function (err) { fail(err.message, err); })
      .then(function () { st.busy = false; btn.disabled = false; });
  });

  $('key-list').addEventListener('click', function (e) {
    var set = current();
    if (!set) { return; }
    var save = e.target.closest('[data-note-save]');
    if (save) {
      var input = save.closest('.keyitem').querySelector('.input');
      saveNote(save.dataset.note, input, save);
      return;
    }
    var revoke = e.target.closest('[data-revoke]');
    if (!revoke) { return; }
    revoke.disabled = true;
    send('DELETE', '/api/sets/' + encodeURIComponent(set.id) + '/keys/' +
      encodeURIComponent(revoke.dataset.revoke)).then(function () {
      return loadState().then(function () { renderKeys(current() || set); });
    }).catch(function () { revoke.disabled = false; });
  });

  $('key-list').addEventListener('keydown', function (e) {
    if (e.key !== 'Enter' || !e.target.classList.contains('input')) { return; }
    e.preventDefault();
    var item = e.target.closest('.keyitem');
    var save = item && item.querySelector('[data-note-save]');
    if (save) { saveNote(save.dataset.note, e.target, save); }
  });

  // кнопка сохранения появляется только когда подпись отличалась от сохранённой
  $('key-list').addEventListener('input', function (e) {
    if (!e.target.classList.contains('input')) { return; }
    var item = e.target.closest('.keyitem');
    var save = item && item.querySelector('[data-note-save]');
    if (!save) { return; }
    var stored = (current() || { keys: [] }).keys.filter(function (k) {
      return k.id === save.dataset.note;
    })[0];
    save.hidden = e.target.value.trim() === ((stored && stored.note) || '');
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
  applyThemeMenu();
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