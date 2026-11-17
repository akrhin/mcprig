/*
  mcprig — панель. Vanilla JS, без зависимостей.
  Данные: /api/state и /api/calls выбранного набора — 5 с, health — 5 с,
  /api/stats — 15 с (пока открыта вкладка), лог — по кнопке и 10 с, /api/env — 30 с;
  скрытая вкладка не опрашивается, листаемую историю вызовов опрос не перезаписывает.
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
      'title.panel': 'mcprig',
      'title.set': 'mcprig — set {name}',
      'a11y.skip': 'Skip to content',
      'top.lang': 'Language',
      'top.theme': 'Appearance',
      'theme.group': 'Theme',
      'theme.scheme': 'Color scheme',
      'theme.scheme98': 'Windows 98 scheme',
      'theme.default': 'Default',
      'theme.win98': 'Windows 98',
      'pal.dark': 'Dark',
      'pal.light': 'Light',
      'pal.nord': 'Nord',
      'pal.gruvbox': 'Gruvbox',
      'pal.w98': 'Classic',
      'pal.w98cool': 'Slate',
      'pal.w98warm': 'Wheat',
      'pal.w98hc': 'High contrast',
      'top.updatedLabel': 'updated',
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
      'rail.sum': '{n} {setsW} · {r} running',
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
      'head.keyOpen': 'set is open',
      'head.copyKey': 'Copy key',
      'head.noKey': 'no key',
      'head.schemaTip': "how much context the set's tool list costs",
      'head.keyMarkTip': "the mark this key shows up under in the statistics: its note, or the key: fingerprint when it has none",
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
      'tabs.servers': 'Servers',
      'tabs.keys': 'Keys',
      'tabs.tools': 'Tools',
      'tabs.calls': 'Calls',
      'tabs.logs': 'Logs',
      'col.set': 'Set',
      'nav.label': 'Panel pages',
      'nav.host': 'Overview',
      'nav.sets': 'Sets',
      'nav.stats': 'Statistics',
      'nav.servers': 'All servers',
      'nav.vars': 'Variables',
      'host.title': 'Host',
      'host.sub': 'what this daemon is serving right now',
      'host.sets': 'Sets by activity',
      'host.latest': 'Latest calls',
      'host.running': 'sets running',
      'host.stopped': 'sets stopped',
      'host.srvOk': 'servers ok',
      'host.srvUnchecked': 'not checked',
      'host.srvFailed': 'servers failing',
      'host.pending': 'tools to approve',
      'host.calls': 'calls · 7 days',
      'host.toolout': 'tool output',
      'host.up': 'up for',
      'host.thState': 'State',
      'host.setsEmpty': 'A set is one MCP endpoint; create one to see it here.',
      'host.callsEmpty': 'Calls appear here as soon as a set serves.',
      'host.tooloutHelp': 'The proxy has no API for the size of /tool-out or how long it keeps files, so this panel shows a dash instead of a guess.',
      'reg.title': 'All servers',
      'reg.count': '{n} {serversW} in the registry',
      'reg.thSets': 'In sets',
      'reg.inNone': 'not attached',
      'reg.hint': 'Health comes from the check of a running set; a server outside every set stays unchecked until you press Check here. An OAuth sign-in happens in the set the server belongs to.',
      'reg.emptyTitle': 'The registry is empty',
      'reg.emptyText': 'A server is one MCP endpoint: a local command or a remote URL. Sets take their members from here.',
      'vars.title': 'Variables',
      'vars.note': 'The names your servers reference as ${NAME}. A value is written here and handed to the set process at start; the panel never reads one back.',
      'vars.name': 'name',
      'vars.value': 'value',
      'vars.store': 'store',
      'vars.storeAuto': 'automatic',
      'vars.storeFile': 'file on this host',
      'vars.needName': 'name: latin letters, digits and _, not starting with a digit',
      'vars.needValue': 'the value cannot be empty',
      'vars.saved': 'value saved for {name} · {source}',
      'vars.savedUnref': '{name} is saved; OpenBao values are listed only when a server references them',
      'vars.unused': 'not used by any server',
      'vars.forgot': '{name} forgotten',
      'vars.restarted': 'restarted: {list}',
      'vars.skippedOne': '{set} stays on its old value: {what}',
      'vars.skippedMissing': 'missing {names}',
      'vars.reloadWarn': 'the value is applied, but the set did not restart: {err}',
      'vars.del': 'forget the value',
      'vars.delConfirm': 'Forget {name}? A running set keeps the value it started with; only sets that no longer need it restart.',
      'vars.honest': 'Values live in OpenBao or in a file only this host can read: the panel shows a name, whether it is set and where it comes from, never a value. A name saved here stays in the list even when no server mentions it, marked as unused.',
      'kpi.calls': 'calls · 7 days',
      'kpi.errRate': 'error rate',
      'kpi.p50': 'p50',
      'kpi.p95': 'p95',
      'setSrv.title': 'Servers',
      'setSrv.add': 'Add server',
      'setSrv.note': '{h} healthy · {f} failed · {u} not checked',
      'setSrv.checking': 'checking {n} {serversW}…',
      'setSrv.thServer': 'Server',
      'setSrv.thType': 'Type',
      'setSrv.thState': 'Health',
      'setSrv.thTools': 'Tools',
      'setSrv.thLast': 'Last call',
      'setSrv.thAct': 'Actions',
      'setSrv.toolsTip': 'tool count comes from a probe of this server',
      'setSrv.lastTip': 'from the last 100 calls',
      'setSrv.emptyTitle': 'This set has no servers',
      'setSrv.emptyText': 'Servers live in the host registry; attach one to give this set tools to serve.',
      'setSrv.attachLabel': 'Add a registry server to this set',
      'setSrv.attach': 'add to set',
      'setSrv.attachNone': 'every server in the registry is already in this set',
      'setSrv.composeHelp': 'The list is saved at once. A running set applies the change without a restart, and a removed server loses its session. An empty set starts and serves no tools.',
      'setSrv.remove': 'remove',
      'setSrv.removeTip': 'detach {name} from the set — the server stays in the registry',
      'setSrv.emptyWarn': 'the set has no servers now: it starts, but serves no tools',
      'setSrv.reloadWarn': 'saved, but the running set did not apply it: {err}',
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
      'calls.noMatchText': 'Calls are there: clear the search or uncheck “errors only”.',
      'calls.more': 'Show more',
      'calls.paused': 'auto-refresh paused',
      'calls.resume': 'resume',
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
      'stats.requests': 'Calls',
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
      'stats.emptyStrong': 'No calls in this period',
      'stats.emptyFilters': 'the filters narrowed the sample to zero — reset them or widen the period',
      'stats.emptyPeriod': 'no calls in the period “{p}”',
      'stats.emptyPeriod2': 'This set recorded no calls in the selected period.',
      'stats.resetFilters': 'Reset filters',
      'stats.unreach': 'no response: {list}',
      'stats.stopped': 'not running: {list}',
      'stats.fail': 'the stats request failed: {msg}',
      'stats.more': '{n} more — narrow the filter',
      'stats.colTip': '{label} · {n} calls',
      'stats.colTipErr': '{label} · {n} calls, {err} errors',
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
      'form.expose': 'expose',
      'form.exposeHint': 'Surfaces the MCP client may read. Without them the client sees the server\u2019s tools only.',
      'form.exposeResources': 'expose resources',
      'form.exposePrompts': 'expose prompts',
      'form.sandbox': 'sandbox',
      'form.sandboxHint': 'mode bwrap | none · onUnavailable deny | warn | allow · network none | allow · empty means the default',
      'form.unset': 'not set',
      'form.ro': 'ro · one per line',
      'form.rw': 'rw · one per line',
      'form.delete': 'delete server',
      'form.editing': 'server · {name}',
      'form.new': 'new server',
      'form.create': 'create',
      'form.deleteConfirm': 'Delete server {name} from the host registry? Used by: {sets}. The tools it brings go with it.',
      'form.deleteEmpty': 'Left without servers: {empty} — such a set starts, but offers no tools.',
      'form.deleteFree': 'Delete server {name} from the host registry? No set uses it.',
      'env.empty': 'no variables',
      'env.set': 'set',
      'env.unset': 'not set',
      'log.tail': 'log tail of the set',
      'log.refresh': 'refresh',
      'log.empty': 'log is empty',
      'srvlog.sub': 'lines that mention this server — the tail of the set log',
      'srvlog.title': 'Log · {server} · {set}',
      'srvlog.fail': 'could not load the log',
      'srv.tools': 'tools',
      'tools.all': 'all servers',
      'tools.sub': 'descriptors the proxy remembers — a changed one waits for your approval',
      'tools.thTool': 'Tool',
      'tools.thState': 'State',
      'tools.thAct': 'Actions',
      'tools.empty': 'this set has not listed any tools yet',
      'tools.ok': 'approved',
      'tools.changed': 'changed',
      'tools.approve': 'approve',
      'tools.fail': 'could not load the tool list',
      'tools.approveFail': 'could not approve the tool',
      'keys.sub': 'A key authenticates one client. It is shown once, right after it is created.',
      'keys.newLabel': 'New key label',
      'keys.profile': 'Profile',
      'keys.profileNone': 'no profile',
      'keys.profileHelp': 'A profile limits the key to the rules the set lists under that name.',
      'keys.profileIs': 'profile: {name}',
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
      'keys.revokedStopped': 'the key is revoked and the set stopped — start it again',
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
      'schema.line': '{bytes} · {tokens} {tok} · {tools} {tool}',
      'srv.edit': 'edit',
      'srv.check': 'check',
      'srv.log': 'log',
      'srv.authorize': 'authorize',
      'auth.title': 'OAuth authorization',
      'auth.waiting': 'Open the link in a browser — the panel waits for the redirect. The set restarts by itself once the token is saved.',
      'auth.link': 'authorization link',
      'auth.open': 'open link',
      'auth.copy': 'copy link',
      'auth.copied': 'link copied',
      'srv.setOff': 'set {name} is not running',
      'srv.unchecked': 'not checked',
      'srv.noResponse': 'no response',
      'srv.healthy': 'Healthy',
      'srv.failed': 'Failed',
      'srv.probeFail': 'probe failed',
      'srv.notFound': 'server {name} is not in the registry',
      'srv.reloadWarn': 'saved, but running sets did not apply the change: {err}',
      'srv.tryStart': 'Start & check',
      'srv.tryStartTip': 'the set is stopped: the check starts the server for a moment, so a running server answers while the set stays down',
      'srv.probeTip': 'the probe starts the server for a moment: the host registry holds no process between calls',
      'srv.probeOk': 'answers, not started',
      'srv.probeOkTip': 'the check started the server for a moment and it answered; the set itself is still stopped',
      'set.del': 'Delete set',
      'set.delConfirm': 'Delete set {name}? Its keys and its call history go with it; the servers stay in the host registry.',
      'set.delFail': 'could not delete the set: {msg}',
      'io.open': 'Import / Export',
      'io.title': 'Import and export',
      'io.sub': 'The host registry as JSON, one set on its own, or servers copied from another client.',
      'io.export': 'Export',
      'io.registry': 'Whole registry',
      'io.download': 'Download JSON',
      'io.copy': 'Copy',
      'io.regNote': '{serversW} · {setsW} · env and headers go out as written in the registry',
      'io.set': 'One set',
      'io.setHelp': 'The servers of the set only, in the shape other clients read. Keys and history stay here.',
      'io.importL': 'Import',
      'io.file': 'JSON file',
      'io.text': 'or paste the JSON',
      'io.ph': '{ "mcpServers": { "x": { "command": "npx", "args": ["-y", "pkg"] } } }',
      'io.importHelp': 'Servers from Claude Desktop and Cursor go in as they are, and so does a registry file. A server already in the registry stays as it is; sets are not imported.',
      'io.peek': '{n} {serversW} to import: {list}',
      'io.peekSets': '{n} {setsW} in the file: sets are made in the panel, so they cannot be imported',
      'io.peekNone': 'no servers in this JSON',
      'io.badJson': 'not JSON: {err}',
      'io.badShape': 'expected the mcpServers object of another client or a registry file',
      'io.doImport': 'import',
      'io.added': 'added: {list}',
      'io.skipped': 'not imported: {list}',
      'io.noNew': 'nothing new: every server is already in the registry',
      'io.fail': 'import failed: {msg}',
      'io.noData': 'the panel returned no registry',
      'prompt.open': 'Prompt',
      'prompt.title': 'Prompt for an agent',
      'prompt.sub': 'One text — an introduction, the set’s tools with their schemas and the task. Copy it to the agent.',
      'prompt.parts': 'Parts',
      'prompt.task': 'Task',
      'prompt.taskPh': 'what the agent has to do with these tools',
      'prompt.intro': 'Introduction · optional',
      'prompt.introPh': 'who the agent is and how it should behave',
      'prompt.introHelp': 'Empty — a short default paragraph about the set is used.',
      'prompt.servers': 'Servers · all when none is chosen',
      'prompt.noServers': 'The set has no servers — the text will list no tools.',
      'prompt.build': 'Build',
      'prompt.result': 'Ready text',
      'prompt.copy': 'copy',
      'prompt.download': 'download .md',
      'prompt.stopped': 'The set is not running: the text gets tool names from the saved list only, without schemas.',
      'prompt.notesLabel': 'notes',
      'prompt.fail': 'could not build the prompt: {msg}',
      'err.offline': 'no connection to the daemon',
      'err.http': 'HTTP {status} · {path}',
      // демо-реестр повторяет правила internal/registry, но говорит на языке панели
      'mock.name': 'name: lowercase latin letters, digits, - and _',
      'mock.exists': 'server {name} already exists',
      'mock.httpUrl': 'http requires url',
      'mock.httpCmd': 'http cannot have a command',
      'mock.stdioCmd': 'stdio requires a command',
      'mock.stdioUrl': 'stdio cannot have a url',
      'mock.sandboxStdio': 'sandbox is only for stdio',
      'mock.sandboxFields': 'sandbox: unknown fields: {list}',
      'mock.untrusted': 'tag untrusted needs a sandbox: a server without isolation will not start',
      'mock.exposeKind': 'expose: unknown kind "{kind}" (allowed {list})',
      'mock.noServer': 'no server {name}',
      'mock.noSet': 'no set {id}',
      'add.nameRequired': 'a name is required'
    },
    ru: {
      'title.panel': 'mcprig',
      'title.set': 'mcprig — набор {name}',
      'a11y.skip': 'Перейти к содержимому',
      'top.lang': 'Язык',
      'top.theme': 'Оформление',
      'theme.group': 'Тема',
      'theme.scheme': 'Цветовая схема',
      'theme.scheme98': 'Схема Windows 98',
      'theme.default': 'Обычная',
      'theme.win98': 'Windows 98',
      'pal.dark': 'Тёмная',
      'pal.light': 'Светлая',
      'pal.nord': 'Nord',
      'pal.gruvbox': 'Gruvbox',
      'pal.w98': 'Классическая',
      'pal.w98cool': 'Холодная серая',
      'pal.w98warm': 'Тёплая бежевая',
      'pal.w98hc': 'Высокая контрастность',
      'top.updatedLabel': 'обновлено',
      'rail.title': 'Наборы',
      'rail.open': 'Открыть список наборов',
      'rail.collapse': 'Свернуть список наборов',
      'rail.expand': 'Развернуть список наборов',
      'rail.empty': 'наборов нет',
      'rail.add': 'Новый набор',
      'rail.name': 'имя',
      'rail.port': 'порт',
      'rail.create': 'Создать',
      'rail.item': '{name} · порт {port} · {state}',
      'rail.sum': '{n} {setsW} · запущено {r}',
      'common.cancel': 'Отмена',
      'common.save': 'Сохранить',
      'common.close': 'Закрыть',
      'common.done': 'Готово',
      'empty.title': 'Наборов пока нет',
      'empty.p': 'Набор — это одна точка входа MCP со своим портом, серверами и ключами.',
      'empty.add': 'Создать первый набор',
      'head.endpoint': 'Адрес',
      'head.copy': 'Копировать',
      'head.copyTip': 'скопировать адрес набора',
      'head.copied': 'скопировано',
      'head.copyFail': 'не удалось скопировать адрес',
      'head.key': 'Ключ',
      'head.keyOpen': 'вход без ключа',
      'head.copyKey': 'Скопировать ключ',
      'head.noKey': 'ключа нет',
      'head.schemaTip': 'сколько контекста стоит перечень инструментов набора',
      'head.keyMarkTip': 'метка ключа набора в статистике: его подпись, а если подписи нет — отпечаток key:',
      'head.servers': 'Серверов',
      'head.tools': 'Инструментов',
      'head.keys': 'Ключи',
      'head.start': 'Запустить',
      'head.restart': 'Перезапустить',
      'head.stop': 'Остановить',
      'head.stopped': 'набор остановлен',
      'head.exited': 'процесс завершился (код {code})',
      'head.exitCode': 'код выхода {code}',
      'head.restarts': 'автоперезапусков: {n}',
      'tabs.overview': 'Обзор',
      'tabs.servers': 'Серверы',
      'tabs.keys': 'Ключи',
      'tabs.tools': 'Инструменты',
      'tabs.calls': 'Вызовы',
      'tabs.logs': 'Лог',
      'col.set': 'Набор',
      'nav.label': 'Страницы панели',
      'nav.host': 'Обзор',
      'nav.sets': 'Наборы',
      'nav.stats': 'Статистика',
      'nav.servers': 'Все серверы',
      'nav.vars': 'Переменные',
      'host.title': 'Хост',
      'host.sub': 'что демон обслуживает прямо сейчас',
      'host.sets': 'Наборы по активности',
      'host.latest': 'Последние вызовы',
      'host.running': 'наборов запущено',
      'host.stopped': 'наборов не запущено',
      'host.srvOk': 'серверов в порядке',
      'host.srvUnchecked': 'не проверено',
      'host.srvFailed': 'серверов с ошибкой',
      'host.pending': 'инструментов на подтверждение',
      'host.calls': 'вызовов за 7 дней',
      'host.toolout': 'выгрузки /tool-out',
      'host.up': 'без перезапуска',
      'host.thState': 'Состояние',
      'host.setsEmpty': 'Набор — это один MCP-эндпоинт; создай его, и он появится здесь.',
      'host.callsEmpty': 'Вызовы появятся, как только набор начнёт отвечать.',
      'host.tooloutHelp': 'У прокси нет API про размер /tool-out и срок хранения файлов, поэтому здесь прочерк, а не догадка.',
      'reg.title': 'Все серверы',
      'reg.count': '{n} {serversW} в реестре',
      'reg.thSets': 'В наборах',
      'reg.inNone': 'не в наборе',
      'reg.hint': 'Здоровье — это проверка запущенного набора; сервер вне наборов остаётся непроверенным, пока не нажать «Проверить» здесь. Вход через OAuth делается в наборе, куда сервер входит.',
      'reg.emptyTitle': 'Реестр пуст',
      'reg.emptyText': 'Сервер — одна точка MCP: локальная команда или удалённый адрес. Наборы берут состав отсюда.',
      'vars.title': 'Переменные',
      'vars.note': 'Имена, которые серверы упоминают как ${NAME}. Значение пишется здесь и уходит процессу набора при старте; обратно панель его не читает.',
      'vars.name': 'имя',
      'vars.value': 'значение',
      'vars.store': 'хранилище',
      'vars.storeAuto': 'автоматически',
      'vars.storeFile': 'файл на этом хосте',
      'vars.needName': 'имя: латинские буквы, цифры и _, не с цифры',
      'vars.needValue': 'значение не может быть пустым',
      'vars.saved': 'значение {name} сохранено · {source}',
      'vars.savedUnref': 'значение {name} сохранено; значения OpenBao видны, только когда сервер на них ссылается',
      'vars.unused': 'не используется ни одним сервером',
      'vars.forgot': 'значение {name} забыто',
      'vars.restarted': 'перезапущены: {list}',
      'vars.skippedOne': '{set} остался на прежнем значении: {what}',
      'vars.skippedMissing': 'нет переменной {names}',
      'vars.reloadWarn': 'значение применено, но набор не перезапустился: {err}',
      'vars.del': 'забыть значение',
      'vars.delConfirm': 'Забыть {name}? Работающий набор остаётся со значением, с которым запустился; перезапускаются только наборы, которым это значение больше не нужно.',
      'vars.honest': 'Значения живут в OpenBao или в файле, который читает только этот хост: видно имя, стоит ли значение и откуда оно берётся, — но не само значение. Имя, сохранённое здесь, остаётся в списке, даже если его не упоминает ни один сервер, — с пометкой «не используется».',
      'kpi.calls': 'вызовов · 7 дней',
      'kpi.errRate': 'доля ошибок',
      'kpi.p50': 'p50',
      'kpi.p95': 'p95',
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
      'setSrv.attachLabel': 'добавить в набор сервер из реестра',
      'setSrv.attach': 'добавить в набор',
      'setSrv.attachNone': 'все серверы реестра уже в наборе',
      'setSrv.composeHelp': 'Состав сохраняется сразу. Запущенный набор применит правку без перезапуска, снятый сервер потеряет соединение. Пустой набор стартует, но инструментов не отдаёт.',
      'setSrv.remove': 'убрать',
      'setSrv.removeTip': 'убрать {name} из набора — сервер останется в реестре',
      'setSrv.emptyWarn': 'набор остался без серверов: стартует, но инструментов не отдаёт',
      'setSrv.reloadWarn': 'сохранено, но работающий набор не применил правку: {err}',
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
      'calls.unavailable': 'метрики вызовов недоступны: счётчики появятся после первого вызова',
      'calls.of': '{n} из {m}',
      'calls.noMatch': 'ничего не найдено — ослабьте фильтр',
      'calls.bad': 'ошибка',
      'calls.ok': 'ок',
      'calls.emptyText': 'Вызовы появятся здесь, как только набор начнёт их принимать.',
      'calls.noMatchText': 'Вызовы есть: сбросьте поиск или снимите «только ошибки».',
      'calls.more': 'Показать ещё',
      'calls.paused': 'автообновление приостановлено',
      'calls.resume': 'возобновить',
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
      'stats.reset': 'Сбросить',
      'stats.approxTip': 'по гистограмме',
      'stats.requests': 'Вызовы',
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
      'stats.noData': 'за период данных нет',
      'stats.noPerSet': 'Нет данных по наборам',
      'stats.noSets': 'разбивка по наборам видна при выборе всех наборов',
      'stats.noClients': 'за период данных по клиентам нет',
      'stats.emptyStrong': 'За период вызовов нет',
      'stats.emptyFilters': 'фильтры сузили выборку до нуля — сбросьте их или расширьте период',
      'stats.emptyPeriod': 'за период «{p}» вызовов нет',
      'stats.emptyPeriod2': 'за выбранный период набор не записал вызовов',
      'stats.resetFilters': 'Сбросить фильтры',
      'stats.unreach': 'нет ответа: {list}',
      'stats.stopped': 'набор не запущен: {list}',
      'stats.fail': 'запрос статистики не удался: {msg}',
      'stats.more': 'ещё {n} — сузьте фильтр',
      'stats.colTip': '{label} · вызовов {n}',
      'stats.colTipErr': '{label} · вызовов {n}, ошибок {err}',
      'side.server': 'сервер',
      'form.sub': 'Изменения попадают в реестр хоста и действуют после перезапуска набора.',
      'form.description': 'описание',
      'form.transport': 'транспорт',
      'form.stdio': 'stdio — локальный процесс',
      'form.http': 'http — удалённый сервер по URL',
      'form.command': 'команда · один аргумент на строку',
      'form.cwd': 'рабочий каталог',
      'form.headers': 'заголовки · K: V',
      'form.env': 'env · KEY=value',
      'form.envAllow': 'envAllow · по одному на строку',
      'form.tags': 'теги · через запятую',
      'form.enabled': 'включён',
      'form.expose': 'отдавать клиенту',
      'form.exposeHint': 'Что клиент MCP читает у этого сервера. Без этого он видит только инструменты.',
      'form.exposeResources': 'отдавать ресурсы',
      'form.exposePrompts': 'отдавать промпты',
      'form.sandbox': 'песочница',
      'form.sandboxHint': 'mode bwrap | none · onUnavailable deny | warn | allow · network none | allow · пусто — значение по умолчанию',
      'form.unset': 'не задано',
      'form.ro': 'ro · по одному на строку',
      'form.rw': 'rw · по одному на строку',
      'form.delete': 'Удалить сервер',
      'form.editing': 'сервер · {name}',
      'form.new': 'новый сервер',
      'form.create': 'Создать',
      'form.deleteConfirm': 'Удалить сервер {name} из реестра хоста? Используется в: {sets}. Инструменты, которые он даёт, уйдут вместе с ним.',
      'form.deleteEmpty': 'Без него пустым останется: {empty} — такой набор запускается, но инструментов не даёт.',
      'form.deleteFree': 'Удалить сервер {name} из реестра хоста? Он не используется ни в одном наборе.',
      'env.empty': 'переменных нет',
      'env.set': 'задана',
      'env.unset': 'не задана',
      'log.tail': 'хвост лога набора',
      'log.refresh': 'Обновить',
      'log.empty': 'лог пуст',
      'srvlog.sub': 'строки, где встречается имя сервера, — хвост лога набора',
      'srvlog.title': 'Лог · {server} · {set}',
      'srvlog.fail': 'не удалось получить лог',
      'srv.tools': 'Инструменты',
      'tools.all': 'все серверы',
      'tools.sub': 'дескрипторы, которые помнит прокси, — изменившийся ждёт вашего одобрения',
      'tools.thTool': 'Инструмент',
      'tools.thState': 'Состояние',
      'tools.thAct': 'Действия',
      'tools.empty': 'набор ещё не показывал инструментов',
      'tools.ok': 'одобрен',
      'tools.changed': 'изменился',
      'tools.approve': 'Одобрить',
      'tools.fail': 'не удалось получить список инструментов',
      'tools.approveFail': 'не удалось одобрить инструмент',
      'keys.sub': 'Ключ даёт доступ одному клиенту. Он показывается один раз — сразу после создания.',
      'keys.newLabel': 'Подпись нового ключа',
      'keys.profile': 'Профиль',
      'keys.profileNone': 'без профиля',
      'keys.profileHelp': 'Профиль ограничивает ключ правилами, которые набор перечисляет под этим именем.',
      'keys.profileIs': 'профиль: {name}',
      'keys.noteFor': 'Подпись для ключа {id}',
      'keys.notePh': 'подпись',
      'keys.note': 'Токен знает только тот, кто его выпустил: панель показывает первые 8 символов. Отзыв ключа действует без перезапуска набора.',
      'keys.issue': 'Выпустить ключ',
      'keys.once': 'Скопируйте ключ сейчас — он больше не покажется',
      'keys.copy': 'Скопировать ключ',
      'keys.revoke': 'Отозвать',
      'keys.revoked': 'отозван {date}',
      'keys.active': 'активен',
      'keys.restartWarn': 'подпись сохранена, но набор не перезапустился: {err}',
      'keys.revokedStopped': 'ключ отозван, набор остановлен — запустите его заново',
      'status.running': 'работает',
      'status.failed': 'сбой',
      'status.stopped': 'остановлен',
      'iso.okTip': 'песочница включена',
      'iso.none': 'без песочницы',
      'iso.rejected': 'конфигурация отклонена',
      'iso.rejectedTip': 'конфигурация отклонена — сервер не запустится',
      'unit.b': 'Б',
      'unit.kb': 'КБ',
      'unit.tokens': 'токенов',
      'unit.tools': 'инструментов',
      'unit.h': 'ч',
      'unit.m': 'м',
      'unit.ms': '{n} мс',
      'schema.line': '{bytes} · {tokens} {tok} · {tools} {tool}',
      'srv.edit': 'Изменить',
      'srv.check': 'Проверить',
      'srv.log': 'Лог',
      'srv.authorize': 'Авторизовать',
      'auth.title': 'Войти через OAuth',
      'auth.waiting': 'Откройте ссылку в браузере — панель ждёт возврата. После сохранения токена набор перезапустится сам.',
      'auth.link': 'ссылка авторизации',
      'auth.open': 'открыть ссылку',
      'auth.copy': 'скопировать ссылку',
      'auth.copied': 'ссылка скопирована',
      'srv.setOff': 'набор {name} не запущен',
      'srv.unchecked': 'не проверен',
      'srv.noResponse': 'нет ответа',
      'srv.healthy': 'Здоров',
      'srv.failed': 'Сбой',
      'srv.probeFail': 'проверка не удалась',
      'srv.notFound': 'сервер {name} не найден в реестре',
      'srv.reloadWarn': 'сохранено, но работающие наборы не применили правку: {err}',
      'srv.tryStart': 'Проверить запуск',
      'srv.tryStartTip': 'набор остановлен: проверка поднимет процесс сервера на время — сервер ответит, а набор останется остановленным',
      'srv.probeTip': 'проверка поднимает процесс сервера на время: между вызовами реестр хоста процесс не держит',
      'srv.probeOk': 'отвечает, не запущен',
      'srv.probeOkTip': 'проверка подняла сервер на время, он ответил; сам набор по-прежнему остановлен',
      'set.del': 'Удалить набор',
      'set.delConfirm': 'Удалить набор {name}? Вместе с ним уйдут его ключи и история вызовов; серверы останутся в реестре хоста.',
      'set.delFail': 'не удалось удалить набор: {msg}',
      'io.open': 'Импорт и экспорт',
      'io.title': 'Импорт и экспорт',
      'io.sub': 'Реестр хоста в JSON, один набор отдельно или серверы, взятые из другого клиента.',
      'io.export': 'Экспорт',
      'io.registry': 'Весь реестр',
      'io.download': 'Скачать JSON',
      'io.copy': 'Скопировать',
      'io.regNote': '{serversW} · {setsW} · env и headers выгружаются так, как записаны в реестре',
      'io.set': 'Один набор',
      'io.setHelp': 'Только серверы набора, в форме, которую читают другие клиенты. Ключи и история остаются здесь.',
      'io.importL': 'Импорт',
      'io.file': 'Файл JSON',
      'io.text': 'или вставьте JSON',
      'io.ph': '{ "mcpServers": { "x": { "command": "npx", "args": ["-y", "pkg"] } } }',
      'io.importHelp': 'Серверы из Claude Desktop и Cursor принимаются как есть, файл реестра — тоже. Сервер, который уже есть в реестре, остаётся как был; наборы не импортируются.',
      'io.peek': 'к импорту {n} {serversW}: {list}',
      'io.peekSets': 'наборов в файле: {n} — наборы делаются в панели, поэтому не импортируются',
      'io.peekNone': 'в этом JSON нет серверов',
      'io.badJson': 'это не JSON: {err}',
      'io.badShape': 'ожидался объект mcpServers другого клиента или файл реестра',
      'io.doImport': 'Импортировать',
      'io.added': 'добавлены: {list}',
      'io.skipped': 'не добавлены: {list}',
      'io.noNew': 'нового нет: все эти серверы уже в реестре',
      'io.fail': 'импорт не удался: {msg}',
      'io.noData': 'панель не отдала реестр',
      'prompt.open': 'Промт',
      'prompt.title': 'Промт для агента',
      'prompt.sub': 'Один текст: вводная, инструменты набора со схемами и задача. Скопируйте его агенту.',
      'prompt.parts': 'Части',
      'prompt.task': 'Задача',
      'prompt.taskPh': 'что агент должен сделать этими инструментами',
      'prompt.intro': 'Вводная · необязательно',
      'prompt.introPh': 'кто агент и как себя вести',
      'prompt.introHelp': 'Пусто — подставится короткий вводный абзац о наборе.',
      'prompt.servers': 'Серверы · без выбора — все',
      'prompt.noServers': 'В наборе нет серверов — в тексте не будет инструментов.',
      'prompt.build': 'Собрать',
      'prompt.result': 'Готовый текст',
      'prompt.copy': 'копировать',
      'prompt.download': 'скачать .md',
      'prompt.stopped': 'Набор остановлен: в текст попадут только имена инструментов из сохранённого списка, без схем.',
      'prompt.notesLabel': 'примечания',
      'prompt.fail': 'не удалось собрать промт: {msg}',
      'err.offline': 'панель не отвечает — проверьте, запущен ли mcprig',
      'err.http': 'HTTP {status} · {path}',
      'mock.name': 'name: строчные латинские буквы, цифры, - и _',
      'mock.exists': 'сервер {name} уже есть',
      'mock.httpUrl': 'http требует url',
      'mock.httpCmd': 'http не может иметь command',
      'mock.stdioCmd': 'stdio требует command',
      'mock.stdioUrl': 'stdio не может иметь url',
      'mock.sandboxStdio': 'sandbox допустим только для stdio',
      'mock.sandboxFields': 'sandbox: неизвестные поля: {list}',
      'mock.untrusted': 'тег untrusted требует sandbox: сервер без изоляции не стартует',
      'mock.exposeKind': 'expose: неизвестный вид "{kind}" (допустимо {list})',
      'mock.noServer': 'нет сервера {name}',
      'mock.noSet': 'нет набора {id}',
      'add.nameRequired': 'Укажите имя набора'
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
    client: { en: ['client', 'clients'], ru: ['клиент', 'клиента', 'клиентов'] },
    line: { en: ['line', 'lines'], ru: ['строка', 'строки', 'строк'] },
    key: { en: ['key', 'keys'], ru: ['ключ', 'ключа', 'ключей'] }
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

  /* ---------- страницы панели ----------
     Адрес называет открытую страницу и то, что на ней открыто: #/ — обзор
     хоста, #/sets — наборы, #/set/<id>/<вкладка> — вкладка набора,
     #/stats/<период> — срез, #/servers и #/vars — остальное. Неизвестная
     вкладка — обзор набора, неизвестный адрес — список наборов: ссылка не
     должна вести в пустоту. */
  var TABS = ['overview', 'servers', 'keys', 'tools', 'calls', 'logs'];

  function pageOf(hash) {
    if (hash === '' || hash === '#' || /^#\/$/.test(hash)) { return 'host'; }
    if (/^#\/set\/[^/]+/.test(hash) || /^#\/sets(\/|$)/.test(hash)) { return 'sets'; }
    if (/^#\/stats\//.test(hash)) { return 'stats'; }
    if (/^#\/servers$/.test(hash)) { return 'servers'; }
    if (/^#\/vars$/.test(hash)) { return 'vars'; }
    return 'sets';
  }

  // один разбор адреса на всех: страница, набор и вкладка достаются из hash
  // одинаково — и на старте, и на hashchange, и когда hash пишет сама панель
  function routeOf(hash) {
    var h = hash || '';
    var m = /^#\/set\/([^/?#]+)(?:\/([^/?#]+))?/.exec(h);
    if (m) {
      var tab = m[2] || 'overview';
      return { page: 'sets', set: m[1], tab: TABS.indexOf(tab) < 0 ? 'overview' : tab };
    }
    var s = /^#\/stats\/([a-z0-9]+)/.exec(h);
    if (s) { return { page: 'stats', period: s[1] }; }
    return { page: pageOf(h) };
  }

  // набор и вкладка из адреса выбираются сразу, данные подтягивает первый /api/state
  var link = routeOf(location.hash);
  var st = {
    sets: [], servers: [], sel: link.set || null,
    health: null, calls: null, kpi: null, env: null, logKey: '', errTimer: 0, busy: false, boot: !!link.set,
    version: '',
    // probe — доказательства строки «Проверить»: они свежее кэша health (15 с)
    probeTools: {}, probeHealth: {}, healthBusy: false,
    tab: link.tab || 'overview', page: link.page, railPref: '',
    // list — страница «Наборы» без набора в адресе: показывается список, а не обзор
    list: link.page === 'sets' && !link.set,
    // host — что показать на обзоре хоста: серверы по каждому набору и
    // дескрипторы инструментов, ожидающие подтверждения
    host: { servers: null, pending: null, busy: false, at: '' },
    // tools — дескрипторы выбранного набора целиком; toolsFilter — выбранный сервер
    tools: null, toolsFilter: '',
    // mock — состояние демо-реестра, правки в нём живут до перезагрузки;
    // srvEditing/srvReturn — какая строка открыта в форме и куда вернётся фокус
    mock: null, mockEnv: null, srvEditing: '', srvReturn: null,
    st: { period: link.period || 'today', set: '', server: '', tool: '', client: '', seq: 0, data: null, facets: null, facetKey: '' },
    // paged — открыта вторая страница вызовов: тикер её не перезаписывает.
    // callsSeq — номер загрузки первой страницы: ответ догрузки из прошлого
    // окна отбрасывается, иначе он допишется в уже заменённый список
    callsMore: { paged: false, busy: false, full: false }, callsSeq: 0
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

  // проценты — Intl: ставит "4.8%" в английском и "4,8 %" в русском; пробел перед знаком убираем
  function pct(fail, all) {
    if (!all) { return '—'; }
    return numFmt({ style: 'percent', minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(fail / all).replace(/\s+%/, '%');
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

  // хвост вызовов переходит через сутки: у не-сегодняшней строки день важнее секунд
  function clockAt(v) {
    var d = new Date(v);
    if (isNaN(d.getTime())) { return '—'; }
    var n = new Date();
    if (d.getFullYear() === n.getFullYear() && d.getMonth() === n.getMonth() && d.getDate() === n.getDate()) {
      return clock(v);
    }
    return fmtCached('clockAt', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(d);
  }

  function day(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) { return '—'; }
    return fmtCached('day', { day: '2-digit', month: '2-digit', year: 'numeric' }).format(d);
  }

  // подпись месячного столбца в «Архиве»: MM.YYYY
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

  // Строка таблицы вызовов: пара «колонка, класс». data-col нужен узкому экрану —
  // там запись читается сверху вниз, а не подпись-значение вразнобой.
  function callRow(cols) {
    var tr = el('tr');
    cols.forEach(function (c) {
      var td = tr.appendChild(el('td'));
      if (c[1]) { td.className = c[1]; }
      td.dataset.col = c[0];
    });
    return tr;
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

  function show(node, on) { if (node) { node.hidden = !on; } }

  // Карта «{id: причина}» из ответа — одной строкой: так приходят reloadErrors и failed.
  function reloadReason(m) {
    if (!m) { return ''; }
    return Object.keys(m).filter(function (k) { return m[k]; })
      .map(function (k) { return k + ': ' + m[k]; }).join('; ');
  }

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

  // Карантин в демо: файл читается один раз, дальше состояние живёт в st.mockTools,
  // иначе кнопка «Одобрить» в демо ничего бы не меняла.
  function mockTools() {
    if (!st.mockTools) {
      st.mockTools = fetch('mock/tools.json', { credentials: 'same-origin' })
        .then(function (r) { return r.json(); });
    }
    return st.mockTools.then(function (d) { return { tools: d.tools || [] }; });
  }

  // Переменные в демо: файл читается один раз, дальше список живёт в st.mockEnv,
  // иначе PUT/DELETE меняли бы ответ, а следующий GET снова отдавал бы файл.
  function mockEnv() {
    if (!st.mockEnv) {
      st.mockEnv = fetch('mock/' + mockName('/api/env') + '.json', { credentials: 'same-origin' })
        .then(function (r) { return r.json(); });
    }
    return st.mockEnv;
  }

  // Демо повторяет контракт /api/calls: limit и before (строго раньше). Иначе
  // «Показать ещё» в демо нечего показать, а адрес запроса расходится с живым.
  function mockCallsPage(data, path) {
    var q = new URLSearchParams(path.split('?')[1] || '');
    var limit = parseInt(q.get('limit'), 10) || 0;
    var before = q.get('before');
    var id = q.get('set');
    var rows = data.calls || [];
    // демон отдаёт строки одного набора, значит и режем окно после сужения
    if (id) { rows = rows.filter(function (r) { return !r.set || r.set === id; }); }
    if (before) {
      rows = rows.filter(function (r) { return new Date(r.time) < new Date(before); });
    }
    data.calls = limit > 0 ? rows.slice(0, limit) : rows;
  }

  // Демон отдаёт строки, где имя сервера стоит отдельным словом, — мок так же:
  // граница слова у него wordByte, а точка в ней тоже буква (serena.mcp ≠ serena).
  function mockServerLog(data, path) {
    var m = /\/servers\/([^/]+)\/logs(\?|$)/.exec(path);
    if (!m) { return; }
    var name = decodeURIComponent(m[1]).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    var re = new RegExp('(^|[^A-Za-z0-9_.-])' + name + '([^A-Za-z0-9_.-]|$)');
    data.logs = (data.logs || []).filter(function (l) { return re.test(l); });
  }

  function get(path) {
    if (MOCK) {
      // реестр в демо-режиме живёт в памяти: правка должна быть видна сразу
      if (st.mock && path.indexOf('/api/state') === 0) { return Promise.resolve(st.mock); }
      // список карантина в демо живёт в памяти: одобрение должно быть видно сразу
      if (/^\/api\/sets\/[^/]+\/tools$/.test(path)) { return mockTools(); }
      // переменные в демо живут в памяти: PUT/DELETE должны менять список сразу
      if (path.indexOf('/api/env') === 0) { return mockEnv(); }
      // срезы считаются из строк, а не читаются готовым файлом: иначе фильтры
      // в демо не на что влиять и цифры врут
      if (path.indexOf('/api/stats') === 0) { return Promise.resolve(mockStats(path)); }
    }
    // запрос мока сохраняет query — в сети видно тот же before, что ушёл бы демону
    var qs = path.indexOf('?') > 0 ? path.slice(path.indexOf('?')) : '';
    var url = MOCK ? 'mock/' + mockName(path) + '.json' + qs : path;
    return fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
      .catch(function () { throw new Error(t('err.offline')); })
      .then(function (r) {
        if (r.status === 404) { return null; }
        if (!r.ok) { throw new Error(t('err.http', { status: r.status, path: path })); }
        return r.json();
      })
      .then(function (d) {
        if (MOCK && d && path.indexOf('/api/state') === 0 && !st.mock) { st.mock = d; }
        if (MOCK && d && path.indexOf('/api/calls') === 0) { mockCallsPage(d, path); }
        if (MOCK && d && /\/servers\/[^/]+\/logs/.test(path)) { mockServerLog(d, path); }
        return d;
      });
  }

  // quiet — у действия своя строка ошибки рядом с кнопкой, и верхняя плашка
  // сказала бы то же второй раз
  function send(method, path, body, quiet) {
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
      if (!quiet) { fail(e.message || String(e)); }
      throw e;
    });
  }

  /* ---------- мок: запись в реестр ----------
     Демо-режим отвечает как реестр: иначе редактор говорит «сохранено» про
     сервер, который настоящий демон отверг бы. Правила повторяют
     internal/registry (имя, транспорт) и internal/config (песочница, теги). */

  var MOCK_SANDBOX_KEYS = ['mode', 'onUnavailable', 'ro', 'rw', 'network', 'memoryMb', 'pids', 'timeoutSec'];
  var MOCK_EXPOSE_KINDS = ['resources', 'prompts'];

  // Отказ демо-реестра — ключ словаря и значения, а не готовая строка: панель
  // повторяет правила internal/registry, но говорит на языке интерфейса.
  function mockServerErr(inp, name, exists) {
    if (!/^[a-z0-9][a-z0-9_-]*$/.test(name)) { return { k: 'mock.name' }; }
    if (exists) { return { k: 'mock.exists', v: { name: name } }; }
    var http = inp.transport === 'http';
    if (http && !inp.url) { return { k: 'mock.httpUrl' }; }
    if (http && inp.command && inp.command.length) { return { k: 'mock.httpCmd' }; }
    if (!http && !(inp.command && inp.command.length)) { return { k: 'mock.stdioCmd' }; }
    if (!http && inp.url) { return { k: 'mock.stdioUrl' }; }
    if (http && inp.sandbox) { return { k: 'mock.sandboxStdio' }; }
    if (inp.sandbox) {
      var extra = Object.keys(inp.sandbox).filter(function (k) { return MOCK_SANDBOX_KEYS.indexOf(k) < 0; });
      if (extra.length) { return { k: 'mock.sandboxFields', v: { list: extra.join(', ') } }; }
    }
    if ((inp.tags || []).indexOf('untrusted') >= 0 && !inp.sandbox) {
      return { k: 'mock.untrusted' };
    }
    // та же строгость, что у config.Check: опечатка не должна молча значить «не отдавать»
    var bad = (inp.expose || []).filter(function (k) { return MOCK_EXPOSE_KINDS.indexOf(k) < 0; });
    if (bad.length) {
      return { k: 'mock.exposeKind', v: { kind: bad[0], list: MOCK_EXPOSE_KINDS.join(', ') } };
    }
    return null;
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
    if (body.expose && body.expose.length) { srv.expose = body.expose; }
    if (body.sandbox && !http) { srv.sandbox = body.sandbox; }
    return srv;
  }

  function mockSend(method, path, body) {
    // Одобрение в демо пишет текущий хэш в запись — как демон, только в памяти.
    var am = /^\/api\/sets\/([^/]+)\/tools\/approve$/.exec(path);
    if (am && method === 'POST' && st.mockTools) {
      return st.mockTools.then(function (d) {
        var rows = (d && d.tools) || [];
        var approved = [];
        rows.forEach(function (row) {
          var one = body && body.server && row.server === body.server && row.tool === body.tool;
          if (!one && !(body && body.all)) { return; }
          if (row.state !== 'changed') { return; }
          row.approvedHash = row.hash;
          row.state = 'ok';
          approved.push(row.server + '/' + row.tool);
        });
        return { approved: approved };
      });
    }
    // Состав набора: демо тоже правит его, иначе правка в панели ничем бы не
    // отличалась от сохранённой и таблица врала бы о наборе.
    var tm = /^\/api\/sets\/([^/]+)$/.exec(path);
    if (tm && method === 'PUT' && st.mock) {
      var tset = st.mock.sets.filter(function (s) { return s.id === decodeURIComponent(tm[1]); })[0];
      if (tset && body && Array.isArray(body.servers)) { tset.servers = body.servers.slice(); }
      // ответ как у живой правки: вид набора, а при неудавшемся рестарте рядом
      // failed (internal/manager/api.go, apiSetPut) — в state.json этого поля нет
      var tfail = tset && tset.id === 'lab'
        ? { failed: { lab: 'перезапуск набора lab не прошёл: порт 9108 занят (демо)' } } : null;
      return Promise.resolve(tset && Object.assign({}, tset, tfail) || {});
    }
    // Промт собирается и в демо: пустой ответ показал бы в диалоге «undefined».
    var pm = /^\/api\/sets\/([^/]+)\/prompt$/.exec(path);
    if (pm && method === 'POST' && st.mock) {
      var pset = st.mock.sets.filter(function (s) { return s.id === decodeURIComponent(pm[1]); })[0];
      var use = (body && body.servers && body.servers.length ? body.servers : (pset && pset.servers) || []);
      var head = ((body && body.intro) || 'Ты работаешь с MCP-набором ' + decodeURIComponent(pm[1]) + ' (mcprig).') +
        '\n\n## Инструменты\n';
      var tools = use.map(function (n) { return '\n### ' + n + '\n\n(демо: инструменты не запрошены)\n'; }).join('');
      var out = head + (use.length ? tools : '\n(серверов нет)\n') + '\n## Задача\n\n' + ((body && body.task) || '') + '\n';
      return Promise.resolve({ prompt: out, notes: ['демо: схемы инструментов не запрашиваются'] });
    }
    // Выдача ключа: демо проверяет профиль теми же словами, что демон
    // (internal/manager/api.go, keyProfileOf), и показывает новый ключ сразу.
    var km = /^\/api\/sets\/([^/]+)\/keys$/.exec(path);
    if (km && method === 'POST' && st.mock) {
      var want = String((body && body.profile) || '').trim();
      var kset = st.mock.sets.filter(function (s) { return s.id === decodeURIComponent(km[1]); })[0];
      var names = (kset && kset.profiles) || [];
      if (want && names.indexOf(want) < 0) {
        var msg = names.length
          ? 'неизвестный профиль ' + want + '; доступны: ' + names.slice().sort().join(', ')
          : 'у набора ' + decodeURIComponent(km[1]) + ' нет профилей — задайте profiles в записи набора';
        fail(msg);
        return Promise.reject(new Error(msg));
      }
      var fresh = { id: 'k_' + kset.id + '_' + (kset.keys.length + 1), createdAt: new Date().toISOString() };
      if (want) { fresh.profile = want; }
      kset.keys.push(fresh);
      return Promise.resolve({ token: 'demo_' + fresh.id, key: fresh, set: kset });
    }
    // Подпись ключа в демо тоже сохраняется: иначе выданный ключ тут же
    // терял бы метку, которую панель только что отправила.
    var nm = /^\/api\/sets\/([^/]+)\/keys\/([^/]+)$/.exec(path);
    if (nm && method === 'PATCH' && st.mock) {
      var nset = st.mock.sets.filter(function (s) { return s.id === decodeURIComponent(nm[1]); })[0];
      var keyID = decodeURIComponent(nm[2]);
      (nset ? nset.keys : []).forEach(function (k) {
        if (k.id === keyID) { k.label = String((body && body.label) || ''); }
      });
      return Promise.resolve({ ok: true });
    }
    // Переменная в демо меняется по-настоящему: PUT пишет значение, DELETE убирает
    // строку, и следующий loadEnv отдаёт изменённый список (иначе страница врала бы).
    var em = /^\/api\/env\/([A-Za-z_][A-Za-z0-9_]*)$/.exec(path);
    if (em && (method === 'PUT' || method === 'DELETE')) {
      var envName = em[1];
      return mockEnv().then(function (d) {
        d.vars = d.vars || [];
        var row = d.vars.filter(function (v) { return v.name === envName; })[0];
        if (method === 'DELETE') {
          // ответ как у apiEnvDelete: removed и source — откуда ушло значение
          var had = !!row && !!row.set;
          var was = (row && row.source) || '';
          // строка остаётся, только если на имя ссылается сервер: у сохранённого
          // без ссылок живой /api/env после забывания строку больше не отдаёт
          if (row && row.used === false) {
            d.vars = d.vars.filter(function (v) { return v.name !== envName; });
          } else if (row) { row.set = false; row.source = ''; }
          return { name: envName, removed: had, source: was, restarted: [], reloadErrors: row && row.reloadErrors };
        }
        // source — из store тела, как storeName в secrets.go: local и пусто → file
        var from = String((body && (body.store || body.source)) || 'file');
        var source = from === 'local' || from === 'automatic' ? 'file' : from;
        if (row) { row.set = true; row.source = source; }
        else { d.vars.push({ name: envName, set: true, used: false, source: source }); }
        return { name: envName, set: true, source: source, restarted: [], reloadErrors: row && row.reloadErrors };
      });
    }
    // Жизненный цикл и удаление набора: демо меняет строку, иначе кнопка
    // выглядит мёртвой — глухой ok на такое действие хуже отказа.
    var lf = /^\/api\/sets\/([^/]+)(?:\/(start|stop|restart|reload))?$/.exec(path);
    if (lf && st.mock && (lf[2] || method === 'DELETE')) {
      var sid = decodeURIComponent(lf[1]);
      var one = st.mock.sets.filter(function (s) { return s.id === sid; })[0];
      if (!one) {
        fail(t('mock.noSet', { id: sid }));
        return Promise.reject(new Error(t('mock.noSet', { id: sid })));
      }
      if (!lf[2]) {
        st.mock.sets = st.mock.sets.filter(function (s) { return s.id !== sid; });
        return Promise.resolve({ ok: true });
      }
      var rt = one.runtime || (one.runtime = { id: one.id, port: one.port });
      if (lf[2] === 'stop') {
        rt.running = false;
        delete rt.pid;
      } else {
        // start/restart/reload: набор оживает и получает свежий startedAt — на
        // строке видно, что действие прошло
        rt.running = true;
        rt.pid = rt.pid || 30000 + (sid.length * 7);
        rt.startedAt = new Date().toISOString();
        delete rt.error;
        delete rt.exitCode;
        delete rt.restarts;
      }
      return Promise.resolve({ ok: true, running: rt.running, port: rt.port });
    }
    var m = /^\/api\/servers(?:\/([^/]+))?$/.exec(path);
    if (!m || !st.mock) { return Promise.resolve({ ok: true, ms: 14, tools: [] }); }
    // у POST имя приходит в теле, у PUT/DELETE — в пути
    var name = m[1] ? decodeURIComponent(m[1]) : String((body && body.name) || '');
    var has = function (n) {
      return st.mock.servers.some(function (s) { return s.name === n; });
    };
    var err = null;
    if (method === 'DELETE') {
      if (!has(name)) { err = { k: 'mock.noServer', v: { name: name } }; }
    } else if (method === 'PUT' && !has(name)) {
      err = { k: 'mock.noServer', v: { name: name } };
    } else {
      err = mockServerErr(body || {}, name, method === 'POST' && has(name));
    }
    if (err) {
      var why = t(err.k, err.v);
      fail(why);
      return Promise.reject(new Error(why));
    }
    if (method === 'DELETE') {
      st.mock.servers = st.mock.servers.filter(function (s) { return s.name !== name; });
      st.mock.sets.forEach(function (s) {
        s.servers = (s.servers || []).filter(function (n) { return n !== name; });
      });
    } else {
      if (method === 'POST') {
        st.mock.servers.push(mockServer(name, body || {}));
      } else {
        // PUT сливает тело с записью, как mergeServer в бэкенде: null и пустой
        // массив снимают поле, отсутствующее в теле поле остаётся
        var old = st.mock.servers.filter(function (s) { return s.name === name; })[0];
        var patch = Object.assign({}, old, body || {});
        st.mock.servers = st.mock.servers.map(function (s) {
          return s.name === name ? mockServer(name, patch) : s;
        });
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
      st.version = data.version || '';
      renderChrome();
      renderRail();
      renderServers(st.health);
      fillFilters();
      var ids = st.sets.map(function (s) { return s.id; });
      if (st.list) { st.sel = null; }
      else if (ids.indexOf(st.sel) === -1) { select(ids[0] || null); }
      else if (st.boot) { st.boot = false; select(st.sel, true); }
      else { renderHead(); renderSetServers(); }
      // обзор хоста собирается из того же /api/state, поэтому живёт с ним в такте
      if (st.page === 'host') { refreshHost(); }
    }).catch(function (e) { fail(e.message, e); });
  }

  // Шапка метрик не несёт: наборы и запущенные — в рейле, серверы реестра — в статусбаре.
  function renderChrome() {
    var none = st.sets.length === 0;
    $('status-counts').textContent = fmtN(st.servers.length) + ' ' + word(st.servers.length, 'server');
    $('f-updated').textContent = clock();
    $('f-version').textContent = st.version ? 'mcprig ' + st.version + ' ·' : '—';
    $('rail-count').textContent = fmtN(st.sets.length);
    // шапка набора и его вкладки — только на странице набора: на списке и на
    // соседних страницах тот же набор ни к чему
    var onSets = st.page === 'sets' && !st.list;
    show($('empty-state'), none && (st.list || onSets || st.page === 'host'));
    show($('rail-empty'), none);
    // без наборов список пуст: там уже стоит общий экран «наборов нет»
    show($('sets-list'), !none && st.page === 'sets' && st.list);
    show($('phead'), !none && onSets);
    show($('tabs'), !none && onSets);
    if (st.list) { renderSetsList(); }
    // на списке наборов панели вкладок не показываются вовсе: полотно списка
    // не должно тащить под собой обзор последнего выбранного набора
    applyTab(none || st.list ? null : st.tab);
    renderCrumb();
    setTitle();
    var set = current();
    $('endpoint').textContent = set
      ? 'http://' + (location.hostname || '127.0.0.1') + ':' + set.port + '/mcp' : '—';
    if (none) { renderKpis(null); renderSetServers(); }
    // обзор хоста собирается из того же /api/state, поэтому живёт с ним в одном такте
    if (st.page === 'host') { renderHost(); }
  }

  // Крошка называет то, что открыто: набор — на его странице, иначе страницу
  function renderCrumb() {
    var set = current();
    $('crumb-set').textContent = st.page !== 'sets' ? t('nav.' + st.page)
      : st.list ? t('nav.sets') : set ? (set.name || set.id) : '—';
  }

  // Список наборов: страница «Наборы» отвечает на вопрос «какие наборы есть»,
  // обзор хоста — на вопрос «что на этой машине происходит». Цифры — из /api/state.
  function renderSetsList() {
    var rows = st.sets.slice().sort(function (a, b) {
      return (a.name || a.id) < (b.name || b.id) ? -1 : 1;
    });
    $('sets-note').textContent = cnt(st.sets.length, 'set');
    sync($('sets-body'), rows, function (s) { return s.id; },
      function () {
        var tr = el('tr');
        tr.appendChild(el('td', 'cell-name'));
        tr.appendChild(el('td'));
        tr.appendChild(el('td', 'num'));
        tr.appendChild(el('td', 'num'));
        return tr;
      },
      function (tr, s) {
        var tds = tr.children;
        var nm = tds[0];
        nm.textContent = '';
        nm.dataset.label = t('col.set');
        var a = nm.appendChild(el('a', '', s.name || s.id));
        a.href = setHash(s.id, '');
        a.appendChild(el('span', 'cell-port', ' :' + s.port));
        tds[1].dataset.label = t('host.thState');
        tds[1].textContent = '';
        paintBadge(tds[1].appendChild(el('span', 'badge')), setState(s));
        tds[2].dataset.label = t('head.servers');
        tds[2].textContent = fmtN((s.servers || []).length);
        tds[3].dataset.label = t('tabs.keys');
        tds[3].textContent = fmtN((s.keys || []).length);
      });
  }

  // Переключение страницы: адрес уже сменён ссылкой или hashchange
  function applyPage(name) {
    st.page = name;
    show($('page-host'), name === 'host');
    show($('page-sets'), name === 'sets');
    show($('page-stats'), name === 'stats');
    show($('page-servers'), name === 'servers');
    show($('page-vars'), name === 'vars');
    [].forEach.call(document.querySelectorAll('#pagenav .segbtn'), function (a) {
      if (a.dataset.page === name) { a.setAttribute('aria-current', 'page'); } else { a.removeAttribute('aria-current'); }
    });
    if (name === 'servers') { renderServers(st.health); }
    if (name === 'vars') { loadEnv(); }
    // срез и обзор показываются только на своей странице: их запросы не нужны
    // остальным, а после возврата цифры должны быть свежими
    if (name === 'stats') { fillFilters(); loadStats(); }
    if (name === 'host') { refreshHost(); }
    renderChrome();
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
    var running = st.sets.filter(function (s) { return s.runtime && s.runtime.running; }).length;
    $('rail-sum').textContent = t('rail.sum', {
      n: fmtN(st.sets.length), setsW: word(st.sets.length, 'set'), r: fmtN(running)
    });
    show($('rail-sum'), st.sets.length > 0);
  }

  function healthy(h) { return !!(h && h.healthy === true && !h.unknown); }

  // Три честных состояния и «не проверено»: пустое здоровье — не «ок».
  // probed — ответ проверки у остановленного набора: проверка поднимает процесс,
  // и её результат иначе пропадал бы бесследно
  function badgeState(h, running, name, probed) {
    if (!running) {
      if (probed && h) {
        return h.healthy === false
          ? { cls: 'badge-err', text: t('srv.failed') }
          : { cls: 'badge-ok', text: t('srv.probeOk') };
      }
      return { cls: 'badge-unknown', text: t('srv.setOff', { name: name }), hollow: true };
    }
    if (!h) { return { cls: 'badge-unknown', text: t('srv.unchecked'), hollow: true }; }
    if (h.healthy === false) { return { cls: 'badge-err', text: t('srv.failed') }; }
    if (h.unknown) { return { cls: 'badge-unknown', text: t('srv.unchecked'), hollow: true }; }
    return { cls: 'badge-ok', text: t('srv.healthy') };
  }

  // Один бейдж здоровья на обе таблицы — строка и реестр: цвет и текст живут здесь
  function paintBadge(node, st8) {
    node.className = 'badge ' + st8.cls;
    node.replaceChildren(el('span', 'dot' + (st8.hollow ? ' dot-hollow' : '')),
      document.createTextNode(st8.text));
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
    renderSetTiles();
    var set = current();
    var names = (set && set.servers) || [];
    var setNm = set ? (set.name || set.id) : '';
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
        : t('srv.setOff', { name: setNm });
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
        var log = act.appendChild(el('button', 'btn btn-xs'));
        log.type = 'button';
        log.dataset.log = '';
        var toolsBtn = act.appendChild(el('button', 'btn btn-xs'));
        toolsBtn.type = 'button';
        toolsBtn.dataset.tools = '';
        var edit = act.appendChild(el('button', 'btn btn-xs'));
        edit.type = 'button';
        edit.dataset.edit = '';
        var del = act.appendChild(el('button', 'ghost-icon icon-xs'));
        del.type = 'button';
        del.dataset.del = '';
        del.innerHTML = '<svg class="od-icon ico-16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
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
          var probed = st.probeHealth && st.probeHealth[row.name];
          var st8 = badgeState(h, running, setNm, probed);
          paintBadge(badge, st8);
          badge.title = (h && h.error) || (probed && !running ? t('srv.probeOkTip') : '');
        }
        // у остановленного набора инструментов нет; у запущенного без проверки
        // число неизвестно — «?» не спорит со счётчиком в шапке, «—» спорил
        var tc = st.probeTools[row.name];
        tr.children[3].textContent = !running ? '—' : tc != null ? fmtN(tc) : '?';
        tr.children[4].textContent = last[row.name] ? clockAt(last[row.name]) : '—';
        var check = tr.querySelector('[data-check]');
        // У остановленного набора проверка поднимает процесс: кнопка называет
        // действие тем, что оно делает, а не тем, как оно выглядит.
        check.textContent = running ? t('srv.check') : t('srv.tryStart');
        check.title = running ? '' : t('srv.tryStartTip');
        check.dataset.check = row.name;
        var log = tr.querySelector('[data-log]');
        log.textContent = t('srv.log');
        log.dataset.log = row.name;
        var toolsBtn = tr.querySelector('[data-tools]');
        toolsBtn.textContent = t('srv.tools');
        toolsBtn.dataset.tools = row.name;
        var edit = tr.querySelector('[data-edit]');
        edit.textContent = t('srv.edit');
        edit.dataset.edit = row.name;
        var del = tr.querySelector('[data-del]');
        del.innerHTML = '<svg class="od-icon ico-16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
        del.dataset.del = row.name;
        del.title = t('setSrv.removeTip', { name: row.name });
        del.setAttribute('aria-label', del.title);
      });
    fillAttach();
  }

  // Свободные серверы реестра — те, кого в наборе ещё нет: чужое имя демон
  // отверг бы, а список из одного набора в другой не переносится.
  function fillAttach() {
    var set = current();
    var inSet = (set && set.servers) || [];
    var sel = $('srv-attach');
    var keep = sel.value;
    sel.replaceChildren();
    var free = st.servers.filter(function (s) { return inSet.indexOf(s.name) < 0; });
    free.forEach(function (s) {
      var opt = sel.appendChild(el('option', '', s.name));
      opt.value = s.name;
    });
    if (free.some(function (s) { return s.name === keep; })) { sel.value = keep; }
    show($('srv-compose'), free.length > 0);
    show($('srv-attach-none'), free.length === 0);
  }

  function setSrvErr(msg) {
    var box = $('srv-compose-err');
    box.textContent = msg || '';
    show(box, !!msg);
  }

  // Состав уходит полным списком: демон разбирает тело как patch, и именно
  // полное поле делает правку одной операцией, без чтения текущего состава.
  function saveSetServers(btn, names) {
    var set = current();
    if (!set || st.busy) { return; }
    st.busy = true;
    if (btn) { btn.disabled = true; }
    setSrvErr('');
    send('PUT', '/api/sets/' + encodeURIComponent(set.id), { servers: names }, true)
      .then(function (r) {
        return loadState().then(function () {
          // failed — правка закоммичена, а рестарт набора не прошёл: причина та же,
          // что у reloadError, поэтому одна ветка и один текст
          var why = reloadReason(r && r.failed) || (r && r.reloadError);
          if (why) { setSrvErr(t('setSrv.reloadWarn', { err: why })); }
          // пустой набор законен, но «нет инструментов» — не то, что видно по таблице
          else if (!names.length) { setSrvErr(t('setSrv.emptyWarn')); }
        });
      })
      .catch(function (e) { setSrvErr(e.message); })
      .then(function () { st.busy = false; if (btn) { btn.disabled = false; } });
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
        if (st.page === 'stats') { loadStats(); }
      }
      renderHead();
      return;
    }
    // форма сервера привязана к списку прежнего набора — закрываем без возврата фокуса
    closeSrv(false);
    st.list = false;
    st.sel = id;
    setDelErr('');
    st.health = null;
    st.calls = null;
    st.probeTools = {};
    st.probeHealth = {};
    st.healthBusy = false;
    st.logKey = '';
    // статистика идёт за рельсом: свой фильтр набора начинается с текущего,
    // ручной выбор в списке фильтра живёт до следующего переключения набора
    st.st.set = id || '';
    // Адрес повторяет открытое: набор и его вкладку. Осознанный выбор (клик в
    // рейле, создание набора, сосед после удаления) открывает страницу набора,
    // даже если панель стояла на обзоре хоста; автовыбор первого набора на
    // #/sets и загрузка на #/ адрес не переписывают.
    if (id && (force || location.hash.indexOf('#/set/') === 0)) {
      if (st.page !== 'sets') { applyPage('sets'); }
      var next = setHash(id, st.tab);
      if (location.hash !== next) { history.pushState(null, '', next); }
    }
    setTitle();
    renderRail();
    // шапка и вкладки прятались на списке наборов — возвращаем их вместе с выбором
    renderChrome();
    // после смены набора вкладка показывает уже его данные: плитки, список, лог
    afterTab(st.tab);
    renderSetServers();
    renderHead();
    renderServers(null);
    renderCalls(null);
    renderLog([]);
    fillFilters();
    if (id) { loadHealth(); loadCalls(); loadLog(); loadTools(); }
  }

  // адрес набора: обзор — короткая ссылка (#/set/x), остальные вкладки названы явно
  function setHash(id, tab) {
    return '#/set/' + id + (tab && tab !== 'overview' ? '/' + tab : '');
  }

  // заголовок окна называет страницу, а набор — только на его странице
  function setTitle() {
    var set = st.page === 'sets' && !st.list ? current() : null;
    if (set) {
      var tab = st.tab && st.tab !== 'overview' ? ' · ' + t('tabs.' + st.tab) : '';
      document.title = t('title.set', { name: (set.name || set.id) + tab });
      return;
    }
    document.title = t('title.panel') + ' — ' + t('nav.' + st.page);
  }

  function renderHead() {
    var set = current();
    if (!set) {
      show($('set-error'), false);
      $('k-calls').textContent = $('k-err').textContent = $('k-p50').textContent = $('k-p95').textContent = '—';
      $('schema').textContent = '—';
      $('meta-tools').textContent = '—';
      show($('key-mark'), false);
      return;
    }
    var state = setState(set);
    var name = set.name || set.id;
    $('set-name').textContent = name;
    renderCrumb();
    $('set-port').textContent = ':' + set.port;
    $('set-state').className = 'badge ' + (state.cls === 'ok' ? 'badge-ok'
      : state.cls === 'err' ? 'badge-err' : 'badge-warn');
    $('set-state-t').textContent = state.text;
    $('endpoint').textContent = 'http://' + (location.hostname || '127.0.0.1') + ':' + set.port + '/mcp';
    // только префикс: токена в панели нет и быть не может
    $('key').textContent = set.keyPrefix ? set.keyPrefix + '…'
      : (set.open ? t('head.keyOpen') : t('head.noKey'));
    // «копировать» работает только когда есть что копировать:
    // у открытого набора в #key лежит фраза состояния, а не ключ
    var keyBtn = $('key-copy'), hasKey = !!set.keyPrefix;
    keyBtn.disabled = !hasKey;
    keyBtn.dataset.i18nTitle = hasKey ? 'head.copyKey' : 'head.noKey';
    keyBtn.title = t(keyBtn.dataset.i18nTitle);
    $('keys-count').textContent = cnt((set.keys || []).length, 'key');
    // метка основного ключа — ровно та строка, что печатает статистика
    var mark = $('key-mark');
    show(mark, !!set.keyMark);
    mark.textContent = set.keyMark || '';
    $('meta-servers').textContent = fmtN((set.servers || []).length);
    var schema = (st.calls && st.calls.schema) || null;
    $('meta-tools').textContent = schema ? fmtN(schema.tools) : '—';
    $('schema').textContent = schemaLine(schema);
    var running = !!(set.runtime && set.runtime.running);
    // Stop — главное действие запущенного набора; перезапуск рядом и тише
    $('run').textContent = running ? t('head.stop') : t('head.start');
    $('run').className = running ? 'btn' : 'btn btn-primary';
    show($('restart'), running);

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
    $('k-p50').textContent = tot && tot.calls ? ms(tot.p50ms) : '—';
    $('k-p95').textContent = tot && tot.calls ? ms(tot.p95ms) : '—';
    // прочерк бледнее цифры: пустая полоса не притворяется нулём
    [].forEach.call(document.querySelectorAll('#kpis .kpi'), function (k) {
      var n = k.querySelector('.kpi-num');
      n.classList.toggle('is-muted', !tot || n.textContent === '—');
    });
  }

  /* ---------- обзор набора ----------
     Плитки отвечают на «всё ли в порядке» и ведут туда, где это разбирается
     по частям. Значения — из того же health и того же карантина, что и вкладки. */

  function tile(id, v) {
    var n = $(id);
    if (n) { n.textContent = v; }
  }

  function renderSetTiles() {
    var set = current();
    var rows = (st.health && st.health.servers) || null;
    var ok = 0, failed = 0, rest = 0;
    if (rows) {
      rows.forEach(function (h) {
        if (healthy(h)) { ok++; } else if (h && h.healthy === false) { failed++; } else { rest++; }
      });
    }
    tile('t-ok', rows ? fmtN(ok) : '—');
    tile('t-unchecked', rows ? fmtN(rest) : '—');
    tile('t-failed', rows ? fmtN(failed) : '—');
    tile('t-keys', set ? fmtN((set.keys || []).length) : '—');
    tile('t-pending', st.tools ? fmtN(pendingTools(st.tools)) : '—');
    tile('t-up', set ? uptime(set) : '—');
  }

  /* ---------- обзор хоста ----------
     Отвечает на «что происходит на этой машине», а не «какие есть наборы»:
     наборы, серверы, трафик и самое свежее. Прочерк в плитке означает «демон
     такого поля не отдаёт», а не ноль — догадку в число не превращаем. */

  var HOST_CALLS = 200;
  var HOST_SHOWN = 12;

  // сервер может стоять в нескольких наборах: «ошибка» старше «ок», потому что
  // окно одного набора не должно затирать провал, который видел другой
  function hostServerState(map, name, v) {
    var prev = map[name];
    if (prev === 'failed' || (prev === 'ok' && v !== 'failed')) { return; }
    map[name] = v;
  }

  function refreshHost() {
    // не чаще раза в 8 с: снимок хоста — это N запросов, а /api/state идёт каждые 5
    if (st.host.busy) { return; }
    if (!st.sets.length && !st.servers.length) { return; }
    if (st.host.at && Date.now() - Date.parse(st.host.at) < 8000) { return; }
    st.host.busy = true;
    var sets = st.sets.slice();
    var live = sets.filter(function (s) { return s.runtime && s.runtime.running; });
    var jobs = live.map(function (s) {
      return get('/api/sets/' + encodeURIComponent(s.id) + '/servers')
        .then(function (d) { return (d && d.servers) || []; }).catch(function () { return []; });
    }).concat(sets.map(function (s) {
      return get('/api/sets/' + encodeURIComponent(s.id) + '/tools')
        .then(function (d) { return (d && d.tools) || []; }).catch(function () { return []; });
    })).concat([
      get('/api/stats?set=all&period=7d&bucket=day').catch(function () { return null; }),
      get('/api/calls?limit=' + HOST_CALLS).catch(function () { return null; })
    ]);
    return Promise.all(jobs).then(function (out) {
      var map = {}, pending = 0, i = 0;
      for (; i < live.length; i++) {
        out[i].forEach(function (h) {
          if (!h || !h.name) { return; }
          hostServerState(map, h.name, healthy(h) ? 'ok' : h.healthy === false ? 'failed' : 'unknown');
        });
      }
      for (; i < live.length + sets.length; i++) { pending += pendingTools(out[i]); }
      var stats = out[i], calls = out[i + 1];
      st.host.servers = map;
      st.host.pending = pending;
      st.host.kpi = stats;
      st.host.calls = (calls && calls.calls) || [];
      st.host.at = new Date().toISOString();
      st.host.busy = false;
      renderHost();
    }).catch(function (e) {
      st.host.busy = false;
      fail(e.message, e);
    });
  }

  function renderHost() {
    var sets = st.sets || [];
    var running = 0;
    sets.forEach(function (s) { if (s.runtime && s.runtime.running) { running++; } });
    tile('h-running', fmtN(running));
    tile('h-stopped', fmtN(sets.length - running));
    var map = st.host.servers, ok = 0, failed = 0, rest = 0;
    (st.servers || []).forEach(function (s) {
      var v = map && map[s.name];
      if (v === 'ok') { ok++; } else if (v === 'failed') { failed++; } else { rest++; }
    });
    tile('h-ok', map ? fmtN(ok) : '—');
    tile('h-unchecked', map ? fmtN(rest) : '—');
    tile('h-failed', map ? fmtN(failed) : '—');
    tile('h-pending', st.host.pending == null ? '—' : fmtN(st.host.pending));
    // размера /tool-out и срока хранения в API нет: прочерк честнее выдумки
    tile('h-toolout', '—');
    var tot = st.host.kpi && st.host.kpi.totals;
    tile('h-calls', tot ? fmtN(tot.calls) : '—');
    tile('h-err', tot && tot.calls ? pct(tot.failures, tot.calls) : '—');
    [].forEach.call(document.querySelectorAll('#host-tiles .tile-num'), function (n) {
      n.classList.toggle('is-muted', n.textContent === '—');
    });
    $('host-calls-note').textContent = st.host.at ? clock(st.host.at) : '';
    renderHostSets();
    renderHostCalls();
  }

  // последний вызов по набору — из того же хвоста, что и таблица ниже
  function lastBySet() {
    var map = {};
    (st.host.calls || []).forEach(function (c) {
      var at = new Date(c.time).getTime();
      if (!c.set || isNaN(at)) { return; }
      if (!map[c.set] || map[c.set] < at) { map[c.set] = at; }
    });
    return map;
  }

  function renderHostSets() {
    var map = lastBySet();
    var rows = (st.sets || []).slice().sort(function (a, b) {
      var x = map[a.id] || 0, y = map[b.id] || 0;
      if (x !== y) { return y - x; }
      return (a.name || a.id) < (b.name || b.id) ? -1 : 1;
    });
    show($('host-sets-empty'), rows.length === 0);
    sync($('host-sets-body'), rows, function (s) { return s.id; },
      function () {
        var tr = el('tr');
        tr.appendChild(el('td', 'cell-name'));
        tr.appendChild(el('td'));
        tr.appendChild(el('td', 'cell-time'));
        return tr;
      },
      function (tr, s) {
        var tds = tr.children;
        var nm = tds[0];
        nm.textContent = '';
        nm.dataset.label = t('col.set');
        var a = nm.appendChild(el('a', '', s.name || s.id));
        a.href = setHash(s.id, '');
        a.appendChild(el('span', 'cell-port', ' :' + s.port));
        var stc = setState(s);
        var state = tds[1];
        state.dataset.label = t('host.thState');
        state.textContent = '';
        paintBadge(state.appendChild(el('span', 'badge')), stc);
        // причина простоя — рядом с состоянием, а не в подсказке
        var why = s.runtime && s.runtime.error;
        if (why) {
          var note = state.appendChild(el('span', 'cell-reason'));
          note.textContent = why.length > 60 ? why.slice(0, 60) + '…' : why;
          note.title = why;
        }
        var last = map[s.id];
        var time = tds[2];
        time.dataset.label = t('setSrv.thLast');
        time.textContent = last ? clockAt(new Date(last).toISOString()) : '—';
        time.title = last ? day(new Date(last).toISOString()) + ' ' + clock(new Date(last).toISOString()) : '';
      });
  }

  function renderHostCalls() {
    var rows = (st.host.calls || []).slice(0, HOST_SHOWN);
    show($('host-calls-empty'), rows.length === 0);
    sync($('host-calls-body'), rows, function (c) { return c.time + c.set + c.server + c.tool; },
      function () {
        return callRow([['time', 'cell-time'], ['set', ''], ['server', 'cell-name'],
          ['tool', ''], ['ms', 'num'], ['status', '']]);
      },
      function (tr, c) {
        var tds = tr.children;
        tds[0].dataset.label = t('tail.thTime');
        tds[0].textContent = clockAt(c.time);
        tds[0].title = day(c.time) + ' ' + clock(c.time);
        tds[1].dataset.label = t('col.set');
        tds[1].textContent = '';
        var a = tds[1].appendChild(el('a', '', c.set || '—'));
        a.href = setHash(c.set || '', 'calls');
        tds[2].dataset.label = t('tail.thServer');
        tds[2].textContent = c.server || '—';
        tds[3].dataset.label = t('tail.thTool');
        tds[3].textContent = c.tool || '—';
        tds[4].dataset.label = t('tail.thMs');
        tds[4].className = 'num' + (c.ms > 1000 ? ' slow' : '');
        tds[4].textContent = fmtN(c.ms);
        tds[5].dataset.label = t('tail.thStatus');
        tds[5].textContent = '';
        var badge = tds[5].appendChild(el('span', 'badge ' + (c.failed ? 'badge-err' : 'badge-ok')));
        badge.appendChild(el('span', 'dot'));
        badge.appendChild(document.createTextNode(c.failed ? t('calls.bad') : t('calls.ok')));
        badge.title = c.error || '';
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

  /* ---------- реестр хоста (страница «Все серверы») ---------- */

  function renderServers(health) {
    var set = current();
    var inSet = (set && set.servers) || [];
    var hs = (health && health.servers) || [];
    // список — общий реестр хоста, а не состав набора: новый сервер виден сразу,
    // наборы, куда он входит, живут отдельной колонкой
    var rows = st.servers.map(function (s) {
      var h = hs.filter(function (x) { return x.name === s.name; })[0] || null;
      var mine = inSet.indexOf(s.name) >= 0;
      // здоровье и статус набора — у своего набора, иначе у первого, где сервер стоит:
      // иначе «не проверен» врёт про сервер остановленного набора
      var own = mine ? set : setsOf(s.name)[0] || null;
      return {
        name: s.name, srv: s, health: h, mine: mine, sets: setsOf(s.name),
        own: own ? (own.name || own.id) : '',
        ownRunning: !!(own && own.runtime && own.runtime.running)
      };
    });
    $('srv-note').textContent = t('reg.count', {
      n: fmtN(rows.length), serversW: word(rows.length, 'server')
    });
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
        var hc = tr.appendChild(el('td'));
        hc.dataset.label = t('setSrv.thState');
        hc.appendChild(el('span', 'badge badge-unknown'));
        var tc = tr.appendChild(el('td', 'num'));
        tc.dataset.label = t('setSrv.thTools');
        tc.title = t('setSrv.toolsTip');
        var sc = tr.appendChild(el('td', 'cell-sets'));
        sc.dataset.label = t('reg.thSets');
        sc.appendChild(el('span'));
        var act = tr.appendChild(el('td', 'cell-actions'));
        var check = act.appendChild(el('button', 'btn btn-xs'));
        check.type = 'button';
        check.dataset.check = '';
        var auth = act.appendChild(el('button', 'btn btn-xs'));
        auth.type = 'button';
        auth.dataset.authorize = '';
        var log = act.appendChild(el('button', 'btn btn-xs'));
        log.type = 'button';
        log.dataset.log = '';
        var edit = act.appendChild(el('button', 'btn btn-xs'));
        edit.type = 'button';
        edit.dataset.edit = '';
        var del = act.appendChild(el('button', 'ghost-icon icon-xs'));
        del.type = 'button';
        del.dataset.del = '';
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
        var note = h && h.healthy === false ? (h.error || t('srv.noResponse'))
          : iso ? iso.label : (srv.description || '');
        sub.textContent = note || '';
        sub.hidden = !note;
        sub.title = (h && h.error) || '';
        sub.className = 'cell-sub' + (h && h.healthy === false ? ' cell-iso' : '');
        tr.querySelector('.chip').textContent =
          srv.transport || (srv.command ? 'stdio' : srv.url ? 'http' : '—');
        // Здоровье знает только проверка: своя (строка «Проверить») или проверка
        // набора, в котором сервер стоит. Вне набора он честно непроверен.
        var probed = st.probeHealth[row.name];
        var badge = tr.querySelector('.badge');
        // статус набора своей строки: у запущенного набора проверка даёт «Здоров»,
        // у остановленного — «отвечает, не запущен», как в таблице набора
        paintBadge(badge, probed
          ? badgeState(probed, row.ownRunning, row.own, true)
          : badgeState(h, row.ownRunning, row.own));
        badge.title = (probed && probed.error) || (h && h.error) || (probed ? t('srv.probeOkTip') : '');
        tr.children[3].textContent = st.probeTools[row.name] != null ? fmtN(st.probeTools[row.name]) : '—';
        var setsCell = tr.querySelector('.cell-sets span');
        setsCell.textContent = row.sets.length
          ? row.sets.map(function (s) { return s.name || s.id; }).join(', ')
          : t('reg.inNone');
        var check = tr.querySelector('[data-check]');
        check.textContent = t('srv.check');
        // проверка поднимает процесс на время: об этом должен знать тот, кто жмёт
        check.title = t('srv.probeTip');
        check.dataset.check = row.name;
        // вход по OAuth — у удалённого сервера набора: локальному он не нужен,
        // а вход идёт в наборе, поэтому только для текущего
        var auth = tr.querySelector('[data-authorize]');
        var canAuth = row.mine && !!srv.url;
        auth.hidden = !canAuth;
        auth.textContent = t('srv.authorize');
        auth.dataset.authorize = canAuth ? row.name : '';
        // лог — хвост лога набора: сервер вне наборов своего лога не имеет
        var log = tr.querySelector('[data-log]');
        log.hidden = row.sets.length === 0;
        log.textContent = t('srv.log');
        log.dataset.log = row.name;
        log.dataset.set = row.sets.length ? row.sets[0].id : '';
        var edit = tr.querySelector('[data-edit]');
        edit.textContent = t('srv.edit');
        edit.dataset.edit = row.name;
        var del = tr.querySelector('[data-del]');
        del.innerHTML = '<svg class="od-icon ico-16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
        del.dataset.del = row.name;
        del.title = t('form.delete');
        del.setAttribute('aria-label', del.title);
      });
  }

  /* ---------- вход по OAuth ----------
     Панель отдаёт ссылку в браузер человека и принимает код на /oauth/callback:
     вход идёт в демоне, а не в запросе клиента, поэтому состояние читается
     опросом, а не ожиданием одного долгого ответа. */

  var authFlow = null;
  var authPoll = 0;

  function authPath(id, name) {
    return '/api/sets/' + encodeURIComponent(id) + '/servers/' + encodeURIComponent(name) + '/authorize';
  }

  function showAuth(v) {
    v = v || {};
    var pending = v.state === 'pending' || v.state === 'idle';
    var link = v.url || '';
    $('auth-note').textContent = pending ? t('auth.waiting') : (v.message || '');
    show($('auth-link-row'), !!link);
    show($('auth-open'), !!link);
    show($('auth-copy'), !!link);
    if (link) { $('auth-link').value = link; }
    return pending;
  }

  function pollAuth() {
    var flow = authFlow;
    if (!flow) { return; }
    clearTimeout(authPoll);
    get(authPath(flow.id, flow.name) + '/status').then(function (v) {
      if (authFlow !== flow) { return; }
      if (showAuth(v)) { authPoll = setTimeout(pollAuth, 1500); }
      else if (v && v.state === 'ok') { setTimeout(loadHealth, 1500); }
    }).catch(function () {
      if (authFlow === flow) { authPoll = setTimeout(pollAuth, 3000); }
    });
  }

  function startAuth(name) {
    var set = current();
    if (!set) { return; }
    authFlow = { id: set.id, name: name };
    $('auth-server').textContent = name;
    $('auth-link').value = '';
    showAuth({ state: 'pending' });
    var dlg = $('auth-dlg');
    if (typeof dlg.showModal === 'function' && !dlg.open) { dlg.showModal(); }
    send('POST', authPath(set.id, name), {}).then(function (v) {
      if (!authFlow) { return; }
      showAuth(v);
      pollAuth();
    });
  }

  function closeAuth() {
    authFlow = null;
    clearTimeout(authPoll);
    var dlg = $('auth-dlg');
    if (dlg.open) { dlg.close(); }
  }

  /* ---------- редактор сервера ----------
     Реестр общий для хоста, сервер правится там же, где список: форма накрывает
     панель. Отдельный экран в узком дровере потерял бы и набор, и контекст. */

  function srvForm() { return $('srvform'); }

  // Ошибка живёт в форме, у кнопки, которая её вызвала: верхняя плашка при
  // открытом диалоге остаётся за ним, а длинная форма прокручивает ошибку из вида.
  function srvErr(msg) {
    var box = $('srv-err');
    box.textContent = msg || '';
    show(box, !!msg);
    if (msg) { box.focus({ preventScroll: true }); box.scrollIntoView({ block: 'nearest' }); }
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
    var ex = (srv && srv.expose) || [];
    f.elements.exposeResources.checked = ex.indexOf('resources') >= 0;
    f.elements.exposePrompts.checked = ex.indexOf('prompts') >= 0;
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
    // null, а не пропуск: PUT сливает тело с записью, и снятая галочка иначе
    // оставила бы прежний expose на месте
    var ex = [];
    if (f.elements.exposeResources.checked) { ex.push('resources'); }
    if (f.elements.exposePrompts.checked) { ex.push('prompts'); }
    out.expose = ex.length ? ex : null;
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
      .then(function (r) {
        st.srvReloadErrors = (r && r.reloadErrors) || null;
        return loadState();
      })
      // loadState перерисовывает наборы и шапку, но не список правой панели
      .then(function () { renderServers(st.health); })
      .then(function () {
        // Правка записана, но работающие наборы её не применили: форма
        // остаётся открытой с причиной, а не закрывается молча.
        if (st.srvReloadErrors) {
          srvErr(t('srv.reloadWarn', { err: reloadReason(st.srvReloadErrors) }));
          return;
        }
        closeSrv();
      })
      .catch(function (err) { srvErr(err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  // Наборы, где стоит сервер: confirm называет их поимённо — «исчезнет из наборов»
  // человеку ничего не говорит о том, что именно он теряет.
  function setsOf(name) {
    return st.sets.filter(function (s) { return (s.servers || []).indexOf(name) >= 0; });
  }

  function setsWith(name) {
    return setsOf(name).map(function (s) { return s.name || s.id; });
  }

  // Что будет с наборами при удалении сервера — один текст на форму и на список
  function deleteText(name) {
    var sets = setsWith(name);
    // набор, где он единственный: после удаления в нём не останется ни одного сервера
    var empty = setsOf(name).filter(function (s) { return (s.servers || []).length === 1; })
      .map(function (s) { return s.name || s.id; });
    return !sets.length
      ? t('form.deleteFree', { name: name })
      : t('form.deleteConfirm', { name: name, sets: sets.join(', ') })
        + (empty.length ? ' ' + t('form.deleteEmpty', { empty: empty.join(', ') }) : '');
  }

  function delSrv() {
    var name = st.srvEditing;
    if (!name || st.busy) { return; }
    if (!window.confirm(deleteText(name))) { return; }
    var btn = $('srv-del');
    st.busy = true;
    btn.disabled = true;
    srvErr('');
    send('DELETE', '/api/servers/' + encodeURIComponent(name), null, true)
      .then(function () { return loadState(); })
      .then(function () { renderServers(st.health); })
      .then(function () {
        closeSrv(false);
        $('srv-add').focus();
      })
      .catch(function (err) { srvErr(err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  // то же удаление из списка реестра: ошибка уходит в плашку над содержимым
  function delServerRow(name, btn) {
    if (!name || st.busy) { return; }
    if (!window.confirm(deleteText(name))) { return; }
    st.busy = true;
    btn.disabled = true;
    send('DELETE', '/api/servers/' + encodeURIComponent(name), null, true)
      .then(function () { return loadState(); })
      .then(function () { renderServers(st.health); renderSetServers(); })
      .catch(function (e) { fail(e.message, e); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  /* ---------- удаление набора ----------
     Набор — это его ключи, история и каталог: серверы живут в реестре хоста и
     переживают его. Демон снимает набор с порта до удаления файлов. */

  function setDelErr(msg) {
    var box = $('set-del-err');
    box.textContent = msg || '';
    show(box, !!msg);
    if (msg) { box.focus({ preventScroll: true }); box.scrollIntoView({ block: 'nearest' }); }
  }

  function delSet() {
    var set = current();
    if (!set || st.busy) { return; }
    var name = set.name || set.id;
    if (!window.confirm(t('set.delConfirm', { name: name }))) { return; }
    var btn = $('set-del');
    var wasSel = st.sel === set.id;
    var idx = st.sets.map(function (s) { return s.id; }).indexOf(set.id);
    st.busy = true;
    btn.disabled = true;
    setDelErr('');
    send('DELETE', '/api/sets/' + encodeURIComponent(set.id), null, true)
      .then(function () { return loadState(); })
      .then(function () {
        // сосед по списку: следующий, а если его нет — предыдущий
        if (!wasSel) { return; }
        var next = st.sets[Math.min(Math.max(idx, 0), st.sets.length - 1)];
        if (next) { select(next.id); } else { $('empty-add').focus(); }
      })
      .catch(function (err) { setDelErr(err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  var CALLS_PAGE = 100;

  // ответ по одному набору строк `set` не несёт; мок отдаёт общий файл — сужаем сами
  function callsOf(data, id) {
    if (!data) { return data; }
    var mine = function (r) { return !r.set || r.set === id; };
    data.calls = (data.calls || []).filter(mine);
    data.counters = (data.counters || []).filter(mine);
    return data;
  }

  // низ таблицы вызовов: кнопка «ещё» на полной странице, пометка о паузе — после
  // догрузки. Обе живут в одной строке, поэтому она видна, если видна хоть одна.
  function syncCallsMore() {
    var full = st.callsMore.full, paused = st.callsMore.paged;
    show($('calls-more'), full);
    show($('calls-pause'), paused);
    show($('calls-more-row'), full || paused);
  }

  function loadCalls() {
    if (!st.sel) { return; }
    var id = st.sel, seq = ++st.callsSeq;
    st.callsMore.paged = false;
    return get('/api/calls?set=' + encodeURIComponent(id) + '&limit=' + CALLS_PAGE).then(function (data) {
      if (id !== st.sel || seq !== st.callsSeq) { return; }
      st.calls = callsOf(data, id);
      // «Показать ещё» появляется, только если страница пришла полной
      st.callsMore.full = !!st.calls && st.calls.calls.length >= CALLS_PAGE;
      syncCallsMore();
      loadKpi();
      renderCalls(st.calls);
      renderHead();
      renderSetServers();
    }).catch(function (e) { fail(e.message, e); });
  }

  // Следующая страница: before — время последней строки ровно в том виде, в каком
  // оно пришло, без пересчёта через Date (иначе смещение часового пояса меняет миг).
  function loadMoreCalls() {
    var id = st.sel, data = st.calls;
    if (!id || !data || !data.calls || !data.calls.length || st.callsMore.busy) { return; }
    var before = data.calls[data.calls.length - 1].time;
    // seq не растёт: это не новая первая страница. Если тикер успеет её взять,
    // ответ уйдёт в уже заменённый список — поэтому сверяем номер после ответа
    var seq = st.callsSeq;
    var btn = $('calls-more');
    st.callsMore.busy = true;
    btn.disabled = true;
    return get('/api/calls?set=' + encodeURIComponent(id) + '&limit=' + CALLS_PAGE +
      '&before=' + encodeURIComponent(before)).then(function (page) {
      if (id !== st.sel || seq !== st.callsSeq) { return; }
      var rows = page ? callsOf(page, id).calls : [];
      data.calls = data.calls.concat(rows);
      st.callsMore.paged = true;
      st.callsMore.full = rows.length >= CALLS_PAGE;
      renderCalls(data);
      syncCallsMore();
    }).catch(function (e) {
      if (id !== st.sel || seq !== st.callsSeq) { return; }
      // загруженное остаётся: плашка говорит о неудаче, а не отменяет список
      fail(e.message, e);
    }).then(function () {
      st.callsMore.busy = false;
      btn.disabled = false;
    });
  }

  // «Возобновить» — тот же первый запрос: догруженное уходит, тикер оживает
  function resumeCalls() {
    if (st.callsMore.busy) { return; }
    loadCalls();
  }

  /* ---------- импорт и экспорт ----------
     Обе стороны — один и тот же JSON. Экспорт берёт готовое у демона (секретов в
     нём нет), импорт принимает объект mcpServers — форму Claude Desktop и
     Cursor — или файл реестра. Наборы не импортируются: их делает панель. */

  var ioData = null;

  function ioMsg(msg) {
    var box = $('io-err');
    box.textContent = msg || '';
    show(box, !!msg);
    if (msg) { box.focus({ preventScroll: true }); box.scrollIntoView({ block: 'nearest' }); }
  }

  // имена списком: длинный импорт не должен растягивать строку на весь диалог
  function ioList(names) {
    var head = names.slice(0, 6).join(', ');
    return names.length > 6 ? head + ' … +' + (names.length - 6) : head;
  }

  // Значение сервера из чужого файла: command строкой и args — законная форма
  // Claude Desktop и Cursor, а демон ждёт argv массивом (строку он отклоняет).
  function ioServer(v) {
    if (!v || typeof v !== 'object' || Array.isArray(v)) { return v; }
    var out = {};
    Object.keys(v).forEach(function (k) { out[k] = v[k]; });
    delete out.name;
    if (Array.isArray(out.args)) {
      out.command = (typeof out.command === 'string' ? [out.command] : (out.command || [])).concat(out.args);
      delete out.args;
    }
    return out;
  }

  // что поняли из текста: пары «имя → сервер» и сколько в файле наборов
  function ioParse(text) {
    if (!text) { return { entries: null }; }
    var data;
    try { data = JSON.parse(text); } catch (e) { return { err: t('io.badJson', { err: e.message }) }; }
    var list = [];
    if (data && data.mcpServers && typeof data.mcpServers === 'object') {
      Object.keys(data.mcpServers).forEach(function (n) { list.push([n, ioServer(data.mcpServers[n])]); });
      return { entries: list, sets: 0 };
    }
    if (data && Array.isArray(data.servers)) {
      data.servers.forEach(function (s) {
        if (s && s.name) { list.push([s.name, ioServer(s)]); }
      });
      return { entries: list, sets: Array.isArray(data.sets) ? data.sets.length : 0 };
    }
    return { err: t('io.badShape') };
  }

  // Предпросмотр до отправки: сколько серверов нашлось и какие — импорт меняет
  // реестр хоста, и «сколько» человек должен увидеть заранее.
  function ioPreview() {
    var p = ioParse(($('io-text').value || '').trim());
    var peek = $('io-peek');
    peek.className = 'field-help';
    ioMsg(p.err || '');
    $('io-add').disabled = !!p.err || !p.entries || !p.entries.length;
    if (p.err || !p.entries) { peek.textContent = ''; return p; }
    var names = p.entries.map(function (e) { return e[0]; });
    var line = names.length
      ? t('io.peek', { n: fmtN(names.length), serversW: word(names.length, 'server'), list: ioList(names) })
      : t('io.peekNone');
    if (p.sets) { line += ' · ' + t('io.peekSets', { n: fmtN(p.sets), setsW: word(p.sets, 'set') }); }
    peek.textContent = line;
    return p;
  }

  function ioFillSets() {
    var sel = $('io-set');
    var keep = sel.value;
    sel.replaceChildren();
    st.sets.forEach(function (s) {
      var o = el('option', '', (s.name || s.id) + ' · :' + s.port);
      o.value = s.id;
      sel.appendChild(o);
    });
    sel.disabled = !st.sets.length;
    $('io-set-dl').disabled = !st.sets.length;
    var ids = st.sets.map(function (s) { return s.id; });
    sel.value = ids.indexOf(keep) >= 0 ? keep : ids.indexOf(st.sel) >= 0 ? st.sel : (ids[0] || '');
  }

  function openIo() {
    var dlg = $('io-dlg');
    ioData = null;
    ioFillSets();
    ioMsg('');
    $('io-peek').textContent = '';
    $('io-file').value = '';
    $('io-text').value = '';
    $('io-add').disabled = true;
    $('io-reg-note').textContent = '';
    $('io-reg-dl').disabled = true;
    $('io-reg-copy').disabled = true;
    if (!dlg.open) { dlg.showModal(); }
    get('/api/export/registry').then(function (d) {
      if (!d) { throw new Error(t('io.noData')); }
      ioData = d;
      ioNote();
      $('io-reg-dl').disabled = false;
      $('io-reg-copy').disabled = false;
    }).catch(function (err) { ioMsg(err.message); });
  }

  // состав реестра словами: пересчитывается и при смене языка, пока диалог открыт
  function ioNote() {
    if (!ioData) { return; }
    var n = (ioData.servers || []).length, m = (ioData.sets || []).length;
    $('io-reg-note').textContent = t('io.regNote', {
      serversW: fmtN(n) + ' ' + word(n, 'server'),
      setsW: fmtN(m) + ' ' + word(m, 'set')
    });
  }

  function closeIo() {
    var dlg = $('io-dlg');
    if (dlg.open) { dlg.close(); }
  }

  // mime нужен промту: это .md, а не JSON
  function download(name, data, mime) {
    var body = typeof data === 'string' ? data : JSON.stringify(data, null, 2);
    var url = URL.createObjectURL(new Blob([body], { type: mime || 'application/json' }));
    var a = el('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  }

  /* ---------- промт для агента ----------
     Текст собирает демон (internal/manager/prompt.go): каталог со схемами и
     маска секретов живут у него, а браузер показал бы половину. */

  var promptText = '', promptNotes = [];

  function promptErr(msg) {
    var box = $('prompt-err');
    box.textContent = msg || '';
    show(box, !!msg);
  }

  function promptWarn() {
    var set = current();
    var off = !set || !(set.runtime && set.runtime.running);
    $('prompt-warn').textContent = off ? t('prompt.stopped') : '';
    show($('prompt-warn'), off);
  }

  function promptNote() {
    $('prompt-notes').textContent = promptNotes.length
      ? t('prompt.notesLabel') + ': ' + promptNotes.join(' · ') : '';
    show($('prompt-notes'), promptNotes.length > 0);
  }

  function openPrompt() {
    var set = current();
    if (!set) { return; }
    promptText = '';
    promptNotes = [];
    $('prompt-set').textContent = set.name || set.id;
    $('prompt-task').value = '';
    $('prompt-intro').value = '';
    promptErr('');
    promptWarn();
    promptNote();
    show($('prompt-result'), false);
    var box = $('prompt-servers');
    var names = set.servers || [];
    box.replaceChildren();
    names.forEach(function (name) {
      var lab = el('label', 'check');
      var cb = lab.appendChild(el('input'));
      cb.type = 'checkbox';
      cb.value = name;
      lab.appendChild(document.createTextNode(name));
      box.appendChild(lab);
    });
    show($('prompt-srv-field'), names.length > 0);
    show($('prompt-nosrv'), names.length === 0);
    var dlg = $('prompt-dlg');
    if (!dlg.open) { dlg.showModal(); }
    $('prompt-task').focus();
  }

  function buildPrompt() {
    var set = current();
    if (!set || st.busy) { return; }
    var task = $('prompt-task').value.trim();
    // задача обязательна: демон ответил бы 400, но кнопка не должна обещать
    // сборку, которой не будет
    if (!task) { $('prompt-task').focus(); return; }
    var chosen = [].map.call($('prompt-servers').querySelectorAll('input:checked'),
      function (i) { return i.value; });
    var body = { task: task, intro: $('prompt-intro').value.trim() };
    if (chosen.length) { body.servers = chosen; }
    var btn = $('prompt-build');
    st.busy = true;
    btn.disabled = true;
    promptErr('');
    send('POST', '/api/sets/' + encodeURIComponent(set.id) + '/prompt', body, true)
      .then(function (r) {
        promptText = (r && r.prompt) || '';
        promptNotes = (r && r.notes) || [];
        $('prompt-out').textContent = promptText;
        promptNote();
        show($('prompt-result'), !!promptText);
      })
      .catch(function (e) { promptErr(t('prompt.fail', { msg: e.message })); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  function closePrompt() {
    var dlg = $('prompt-dlg');
    if (dlg.open) { dlg.close(); }
  }


  function ioImport() {
    var p = ioPreview();
    if (!p.entries || !p.entries.length) { return; }
    var body = { mcpServers: {} };
    p.entries.forEach(function (e) { body.mcpServers[e[0]] = e[1]; });
    var btn = $('io-add');
    btn.disabled = true;
    send('POST', '/api/import', body)
      .then(function (r) {
        var added = (r && r.added) || [];
        var skipped = (r && r.skipped) || [];
        var parts = [added.length ? t('io.added', { list: ioList(added) }) : t('io.noNew')];
        if (skipped.length) {
          // причину даёт демон, по-русски — её не переводим
          parts.push(t('io.skipped', {
            list: skipped.map(function (s) { return s.name ? s.name + ' (' + s.reason + ')' : s.reason; }).join('; ')
          }));
        }
        $('io-peek').textContent = parts.join(' · ');
        $('io-peek').className = 'form-alert form-alert-ok';
        // реестр хоста изменился — и селект наборов, и дровер должны знать сразу
        ioData = null;
        return loadState();
      })
      .catch(function (err) { ioMsg(t('io.fail', { msg: err.message })); })
      .then(function () { btn.disabled = false; });
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
      var time = tr.appendChild(el('td', 'cell-time', clockAt(c.time)));
      time.dataset.label = t('tail.thTime');
      time.title = day(c.time) + ' ' + clock(c.time);
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
      // Причина провала видна в строке: полный текст — в title, чтобы длинное
      // тело ошибки не растягивало таблицу.
      if (c.failed && c.error) {
        var why = el('span', 'call-err', ' · ' + (c.error.length > 80 ? c.error.slice(0, 80) + '…' : c.error));
        why.title = c.error;
        stc.appendChild(why);
      }
      frag.appendChild(tr);
    });
    box.replaceChildren(frag);

    $('calls-count').textContent = rows.length === all.length
      ? cnt(all.length, 'call') : t('calls.of', { n: fmtN(rows.length), m: fmtN(all.length) });
    show(note, rows.length === 0);
    // фильтр врёт о причине: пустой набор и «всё отсеяно» — разные вещи
    var filtered = !!(term || onlyErr);
    note.querySelector('.empty-title').textContent = filtered ? t('calls.noMatch') : t('tail.empty');
    note.querySelector('.empty-text').textContent = filtered ? t('calls.noMatchText') : t('calls.emptyText');
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
        var del = li.appendChild(el('button', 'ghost-icon icon-xs'));
        del.type = 'button';
        del.dataset.envdel = '';
        del.innerHTML = '<svg class="od-icon ico-16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
        return li;
      },
      function (li, v) {
        var code = li.querySelector('code');
        code.textContent = v.name;
        code.title = v.name;
        li.querySelector('.v-state').textContent = (v.set ? t('env.set') : t('env.unset')) +
          (v.source ? ' · ' + v.source : '') +
          (v.used === false ? ' · ' + t('vars.unused') : '') +
          (!v.set && st.envRemoteError ? ' · ' + st.envRemoteError : '');
        var del = li.querySelector('[data-envdel]');
        del.dataset.envdel = v.name;
        del.title = t('vars.del');
        del.setAttribute('aria-label', t('vars.del') + ' · ' + v.name);
      });
  }

  // Плашка под формой: строка исхода одна, ошибка и «сохранено» не стоят рядом.
  // skipped — наборы, оставленные на старом окружении: это предупреждение, не ошибка.
  // reload — карта reloadErrors из ответа: значение записано, но набор не поднялся
  function envNote(ok, text, skipped, reload) {
    show($('env-err'), !ok);
    show($('env-msg'), ok);
    $(ok ? 'env-msg' : 'env-err').textContent = text;
    var warn = (ok && skipped && skipped.length ? skipped : []).map(function (s) {
      // причина приходит и машинно (missing), и текстом (reason): фразу
      // собираем на языке читателя, а reason нужен тем, кто зовёт API напрямую
      var what = s.missing && s.missing.length
        ? t('vars.skippedMissing', { names: s.missing.join(', ') })
        : (s.reason || '');
      return t('vars.skippedOne', { set: s.set, what: what });
    });
    var why = reloadReason(ok && reload);
    if (why) { warn.push(t('vars.reloadWarn', { err: why })); }
    show($('env-warn'), warn.length > 0);
    $('env-warn').textContent = warn.join(' · ');
  }

  function saveEnv(e) {
    e.preventDefault();
    var name = $('env-name').value.trim();
    var value = $('env-value').value;
    var store = $('env-store').value;
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) { envNote(false, t('vars.needName')); $('env-name').focus(); return; }
    if (!value) { envNote(false, t('vars.needValue')); $('env-value').focus(); return; }
    var btn = $('env-save');
    if (st.busy) { return; }
    st.busy = true;
    btn.disabled = true;
    send('PUT', '/api/env/' + encodeURIComponent(name), { value: value, store: store })
      .then(function (r) {
        r = r || {};
        envNote(true, t('vars.saved', { name: r.name || name, source: r.source || store || 'auto' }) +
          (r.restarted && r.restarted.length ? ' · ' + t('vars.restarted', { list: r.restarted.join(', ') }) : ''), r.skipped, r.reloadErrors);
        $('env-value').value = '';
        return loadEnv().then(function () {
          // значение записано, но имени нет в списке: его пока не упоминает ни один сервер
          var seen = (st.env || []).some(function (v) { return v.name === (r.name || name); });
          if (!seen) { envNote(true, t('vars.saved', { name: r.name || name, source: r.source || 'auto' }) + ' · ' + t('vars.savedUnref', { name: name }), r.skipped, r.reloadErrors); }
        });
      })
      .catch(function (err) { envNote(false, err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  function delEnv(name, btn) {
    if (st.busy || !name) { return; }
    if (!window.confirm(t('vars.delConfirm', { name: name }))) { return; }
    st.busy = true;
    btn.disabled = true;
    // без store: значение забывается во всех хранилищах, а не только в одном
    send('DELETE', '/api/env/' + encodeURIComponent(name), null, true)
      .then(function (r) {
        r = r || {};
        envNote(true, t('vars.forgot', { name: r.name || name })
          + (r.restarted && r.restarted.length ? ' · ' + t('vars.restarted', { list: r.restarted.join(', ') }) : ''), r.skipped, r.reloadErrors);
        return loadEnv();
      })
      .catch(function (err) { envNote(false, err.message); })
      .then(function () { st.busy = false; btn.disabled = false; });
  }

  function loadLog() {
    if (!st.sel) { return; }
    var id = st.sel;
    return get('/api/sets/' + id + '/logs?lines=200').then(function (data) {
      if (id !== st.sel) { return; }
      renderLog((data && data.logs) || []);
    }).catch(function (e) { fail(e.message, e); });
  }

  // тон строки общий у обоих логов: слово-маркер важнее того, кто пишет лог
  function logClass(line) {
    return /\b(error|err|fatal|panic)\b/i.test(line) ? 'is-error' : /\bwarn(ing)?\b/i.test(line) ? 'is-warn' : '';
  }

  function renderLog(lines) {
    var key = lines.join('\n');
    if (key === st.logKey) { return; }
    st.logKey = key;
    $('log-count').textContent = cnt(lines.length, 'line');
    show($('log'), lines.length > 0);
    show($('log-empty'), lines.length === 0);
    sync($('log'), lines, function (l, i) { return String(i); },
      function () { return el('div', 'logline'); },
      function (row, line) {
        var cls = logClass(line);
        row.className = 'logline' + (cls ? ' ' + cls : '');
        row.textContent = line;
      });
  }

  /* ---------- лог одного сервера ----------
     Отдельного файла у сервера нет: демон отдаёт строки лога набора, где имя
     сервера встречается отдельным словом, поэтому дровер читает тот же маршрут. */

  var srvLog = { name: '', set: '', seq: 0 };

  function openSrvLog(name, setId) {
    // набор — тот, где сервер стоит: со страницы реестра он приходит в dataset
    var id = setId || (current() && current().id);
    if (!id) { return; }
    srvLog.name = name;
    srvLog.set = id;
    srvLog.returnFocus = document.activeElement;
    $('srvlog-h').textContent = t('srvlog.title', { server: name, set: id });
    $('srvlog-err').hidden = true;
    var dlg = $('srvlog-dlg');
    if (typeof dlg.showModal === 'function' && !dlg.open) { dlg.showModal(); }
    $('srvlog-refresh').focus();
    loadSrvLog();
  }

  function closeSrvLog() {
    var dlg = $('srvlog-dlg');
    if (!dlg.open) { return; }
    dlg.close();
    srvLog.name = '';
    srvLog.seq++;
    var back = srvLog.returnFocus;
    srvLog.returnFocus = null;
    if (back && document.contains(back) && back.offsetParent !== null) { back.focus(); }
  }

  function loadSrvLog() {
    if (!srvLog.name) { return; }
    var name = srvLog.name, id = srvLog.set, seq = ++srvLog.seq;
    var btn = $('srvlog-refresh');
    btn.disabled = true;
    $('srvlog-err').hidden = true;
    return get('/api/sets/' + encodeURIComponent(id) + '/servers/' + encodeURIComponent(name) + '/logs?lines=200')
      .then(function (data) {
        if (seq !== srvLog.seq) { return; }
        renderSrvLog((data && data.logs) || []);
      })
      .catch(function (e) {
        if (seq !== srvLog.seq) { return; }
        // уже показанные строки остаются: старый хвост полезнее пустого места
        var box = $('srvlog-err');
        box.textContent = t('srvlog.fail') + ' · ' + e.message;
        box.hidden = false;
      })
      .then(function () { if (seq === srvLog.seq) { btn.disabled = false; } });
  }

  function renderSrvLog(lines) {
    var box = $('srvlog');
    $('srvlog-count').textContent = cnt(lines.length, 'line');
    show(box, lines.length > 0);
    show($('srvlog-empty'), lines.length === 0);
    // текст лога — только textContent: строки приходят от процессов, не от панели
    box.replaceChildren();
    lines.forEach(function (line) {
      var row = el('div', 'logline', line);
      var cls = logClass(line);
      if (cls) { row.className += ' ' + cls; }
      box.appendChild(row);
    });
    box.scrollTop = box.scrollHeight;
  }

  /* ---------- карантин дескрипторов ----------
     Панель показывает то, что помнит прокси: новый инструмент одобрен сам,
     изменившийся ждёт человека, иначе вызов нижестоящего сервера отбит.
     Демон отдаёт дескрипторы всего набора, поэтому фильтр по серверу —
     это выбор в списке, а не отдельный запрос. */

  var toolsSeq = 0;

  function fillToolsServers(rows) {
    var sel8 = $('tools-server');
    var names = [], seen = {};
    ((current() && current().servers) || []).forEach(function (n) { seen[n] = 1; names.push(n); });
    (rows || []).forEach(function (r) { if (!seen[r.server]) { seen[r.server] = 1; names.push(r.server); } });
    if (st.toolsFilter && !seen[st.toolsFilter]) { names.push(st.toolsFilter); }
    var want = st.toolsFilter;
    sel8.textContent = '';
    var all = sel8.appendChild(el('option'));
    all.value = '';
    all.textContent = t('tools.all');
    names.sort().forEach(function (n) {
      var o = sel8.appendChild(el('option'));
      o.value = n;
      o.textContent = n;
    });
    sel8.value = names.indexOf(want) >= 0 ? want : '';
    st.toolsFilter = sel8.value;
  }

  function loadTools() {
    if (!st.sel) { return; }
    var id = st.sel, seq = ++toolsSeq;
    var btn = $('tools-refresh');
    btn.disabled = true;
    $('tools-err').hidden = true;
    return get('/api/sets/' + encodeURIComponent(id) + '/tools')
      .then(function (data) {
        if (seq !== toolsSeq || id !== st.sel) { return; }
        st.tools = (data && data.tools) || [];
        fillToolsServers(st.tools);
        renderTools();
      })
      .catch(function (e) {
        if (seq !== toolsSeq) { return; }
        var box = $('tools-err');
        box.textContent = t('tools.fail') + ' · ' + e.message;
        box.hidden = false;
      })
      .then(function () { if (seq === toolsSeq) { btn.disabled = false; } });
  }

  // строки, ожидающие подтверждения: пара «сервер + инструмент» считается один
  // раз, иначе таблица одного сервера раздувает счётчик обзора
  function pendingTools(rows) {
    var seen = {}, n = 0;
    (rows || []).forEach(function (r) {
      if (r.state !== 'changed') { return; }
      var k = r.server + '/' + r.tool;
      if (!seen[k]) { seen[k] = 1; n++; }
    });
    return n;
  }

  function renderTools() {
    renderSetTiles();
    var rows = (st.tools || []).filter(function (r) { return !st.toolsFilter || r.server === st.toolsFilter; });
    $('tools-count').textContent = cnt(rows.length, 'tool');
    show($('tools-empty'), rows.length === 0);
    sync($('lock-body'), rows, function (r) { return r.server + '/' + r.tool; },
      function () {
        var tr = el('tr');
        var nm = tr.appendChild(el('td', 'cell-name'));
        nm.dataset.label = t('tools.thTool');
        var stateCell = tr.appendChild(el('td'));
        stateCell.dataset.label = t('tools.thState');
        stateCell.appendChild(el('span', 'badge'));
        var act = tr.appendChild(el('td', 'cell-actions'));
        var btn = act.appendChild(el('button', 'btn btn-xs'));
        btn.type = 'button';
        btn.dataset.approve = '';
        return tr;
      },
      function (tr, row) {
        tr.dataset.tool = row.tool;
        tr.dataset.server = row.server;
        var nm = tr.querySelector('.cell-name');
        // без фильтра в имени нужен и сервер: одно имя инструмента живёт у разных
        nm.textContent = st.toolsFilter ? row.tool : row.server + ' · ' + row.tool;
        nm.title = row.server + ' / ' + row.tool;
        var changed = row.state === 'changed';
        var badge = tr.querySelector('.badge');
        badge.className = 'badge ' + (changed ? 'badge-warn' : 'badge-ok');
        badge.textContent = changed ? t('tools.changed') : t('tools.ok');
        var btn = tr.querySelector('[data-approve]');
        btn.textContent = t('tools.approve');
        btn.dataset.approve = row.tool;
        // одобрять нечего у одобренного: кнопка остаётся, но не жмётся вслепую
        btn.hidden = !changed;
        btn.title = changed ? t('tools.approve') : '';
      });
  }

  // строка сервера в наборе: «инструменты» этого сервера — вкладка с фильтром
  function openTools(server) {
    if (!st.sel) { return; }
    st.toolsFilter = server || '';
    setTab('tools', true);
  }

  function approveTool(server, tool, btn) {
    if (!st.sel) { return; }
    if (btn) { btn.disabled = true; }
    return send('POST', '/api/sets/' + encodeURIComponent(st.sel) + '/tools/approve',
      { server: server, tool: tool })
      .then(function () { return loadTools(); })
      .catch(function (e) {
        fail(t('tools.approveFail') + ' · ' + e.message, e);
      })
      .then(function () { if (btn) { btn.disabled = false; } });
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
    // остановленный набор — состояние, а не отказ: тон предупреждающий, а не красный
    var live = unreach.some(function (id) {
      var s = st.sets.filter(function (x) { return x.id === id; })[0];
      return !!(s && s.runtime && s.runtime.running);
    });
    show($('stats-unreach'), unreach.length > 0);
    $('stats-unreach').className = 'form-alert' + (live ? '' : ' form-alert-warn');
    $('stats-unreach').textContent = unreach.length
      ? t(live ? 'stats.unreach' : 'stats.stopped', { list: unreach.join(', ') }) : '';

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

    renderBreakdown($('tools-body'), d.tools, 'tool', t('stats.thTool'), 'server', $('stats-tools-empty'), 11);
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
    TABS.forEach(function (tab) {
      var btn = $('tabbtn-' + tab);
      if (!btn) { return; }
      var on = tab === name;
      btn.setAttribute('aria-selected', on ? 'true' : 'false');
      btn.tabIndex = on ? 0 : -1;
      show($('tab-' + tab), on);
    });
  }

  /* ---------- вкладки набора ----------
     У каждой вкладки свой адрес (#/set/<id>/keys): ссылку можно переслать,
     положить в закладки и вернуть кнопкой «назад» — история помнит переход. */

  function setTab(name, push) {
    if (TABS.indexOf(name) < 0) { name = 'overview'; }
    st.tab = name;
    applyTab(name);
    setTitle();
    afterTab(name);
    // адрес пишется только на странице набора — на соседних вкладок не видно.
    // «Наборы» из адреса остаются наборной страницей: там живёт страница, а не набор
    if (st.page === 'sets' && st.sel && location.hash !== '#/sets') {
      var hash = setHash(st.sel, name);
      if (location.hash !== hash) {
        if (push) { history.pushState(null, '', hash); } else { history.replaceState(null, '', hash); }
      }
    }
  }

  // afterTab — что догрузить для показанной вкладки. Одно место на все вызовы:
  // первый показ набора и переключение вкладки идут этим же путём
  function afterTab(name) {
    if (!st.sel) { return; }
    if (name === 'overview') { renderSetTiles(); }
    if (name === 'servers') { renderSetServers(); }
    if (name === 'keys') { openKeys(); }
    if (name === 'tools') { renderTools(); loadTools(); }
    if (name === 'calls') { renderCalls(st.calls); }
    if (name === 'logs') { loadLog(); }
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

  /* ---------- рейл ---------- */

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
    show($('scrim'), false);
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
    if (w >= 860) { closeRail(); }
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
      // состояние уже перерисовано: подпись не должна откатываться к старой
      if (btn.textContent === '…') { btn.textContent = label; }
    });
  }

  // вход во вкладку ключей: список, профили и пустая плашка нового ключа —
  // выданный ключ показывается один раз и при возврате во вкладку уже скрыт
  function openKeys() {
    var set = current();
    if (!set) { return; }
    renderKeys(set);
    fillKeyProfiles(set);
    show($('new-key-box'), false);
    show($('keys-warn'), false);
    $('key-label').value = '';
  }

  // Профиль — сужение прав, записанное в наборе: панель знает только имена,
  // правила читает демон. Нет профилей — нет и селектора.
  function fillKeyProfiles(set) {
    var names = (set && set.profiles) || [];
    var sel = $('key-profile');
    sel.textContent = '';
    if (!names.length) { show($('key-profile-field'), false); return; }
    var none = sel.appendChild(el('option', '', t('keys.profileNone')));
    none.value = '';
    names.forEach(function (name) {
      var opt = sel.appendChild(el('option', '', name));
      opt.value = name;
    });
    sel.value = '';
    show($('key-profile-field'), true);
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
        head.appendChild(el('span', 'badge badge-unknown key-badge'));
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
        var badge = li.querySelector('.key-badge');
        badge.textContent = k.profile || '';
        badge.hidden = !k.profile;
        badge.title = k.profile ? t('keys.profileIs', { name: k.profile }) : '';
        badge.setAttribute('aria-label', k.profile ? t('keys.profileIs', { name: k.profile }) : '');
        li.querySelector('.key-sub').textContent = revoked
          ? day(k.createdAt) + ' · ' + t('keys.revoked', { date: day(k.revokedAt) })
          : day(k.createdAt) + ' · ' + t('keys.active');
        var input = li.querySelector('.key-note .input');
        input.value = k.label || '';
        input.placeholder = t('keys.notePh');
        input.disabled = revoked;
        input.dataset.note = k.id;
        input.setAttribute('aria-label', t('keys.noteFor', { id: k.id }));
        var save = li.querySelector('[data-note-save]');
        save.textContent = t('common.save');
        save.dataset.note = k.id;
        save.hidden = revoked || input.value.trim() === (k.label || '');
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
      { label: input.value.trim() }).then(function (r) {
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
    renderSetTiles();
    renderSetServers();
    renderServers(st.health);
    renderCalls(st.calls);
    renderHost();
    renderEnv();
    // список ключей перерисовывается, а выданный токен в #new-key-box остаётся:
    // смена языка не должна съедать секрет, показанный один раз
    renderKeys(current() || {});
    fillKeyProfiles(current());
    fillFilters();
    renderStats(st.st.data);
    applyRail();
    applyThemeMenu();
    // у диалога импорта текст собирается кодом, а title его не перерисует
    if ($('io-dlg').open) { ioNote(); ioPreview(); }
    if ($('prompt-dlg').open) { promptWarn(); promptNote(); }
  };

  /* ---------- привязки ---------- */

  $('rail-sets').addEventListener('click', function (e) {
    var row = e.target.closest('.set-item');
    if (!row) { return; }
    // выбор в рейле — шаг истории: «назад» возвращает на прежний набор
    select(row.dataset.id, true);
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

  $('scrim').addEventListener('click', closeRail);

  // клик по вкладке — шаг истории: «назад» возвращает на прежнюю вкладку
  $('tabs').addEventListener('click', function (e) {
    var btn = e.target.closest('.tab');
    if (btn) { setTab(btn.dataset.tab, true); }
  });
  // плитки обзора набора — кнопки: ведут на свою вкладку той же страницы
  $('set-tiles').addEventListener('click', function (e) {
    var tile = e.target.closest('[data-goto]');
    if (tile) { setTab(tile.dataset.goto, true); }
  });
  $('tabs').addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') { return; }
    var i = TABS.indexOf(st.tab) + (e.key === 'ArrowRight' ? 1 : -1);
    if (i < 0) { i = TABS.length - 1; }
    if (i >= TABS.length) { i = 0; }
    setTab(TABS[i], true);
    $('tabbtn-' + TABS[i]).focus();
  });

  $('s-period').addEventListener('keydown', function (e) {
    arrowNav(e, this, function (b) { setPeriod(b.dataset.period); });
  });

  // Tab не уходит ни в контент под дровером, ни за открытую форму сервера
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      if ($('theme-menu').hidden === false) { closeThemeMenu(true); return; }
      if ($('srv-dlg').open) { closeSrv(); return; }
      if ($('srvlog-dlg').open) { closeSrvLog(); return; }
      if (document.body.classList.contains('rail-open')) { closeRail(); }
      return;
    }
    if (e.key !== 'Tab') { return; }
    var root = $('srv-dlg').open ? $('srv-dlg') : $('srvlog-dlg').open ? $('srvlog-dlg') : null;
    if (!root) { return; }
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
    if (st.page !== 'sets') { applyPage('sets'); }
    if (st.tab !== 'calls') { setTab('calls', true); }
    $('q').focus();
  });

  /* Адрес — единственный источник правды о том, что открыто: ссылка, «назад»
     и «вперёд» идут одним путём. route() читает hash и приводит панель к нему. */
  function route() {
    var r = routeOf(location.hash);
    // адрес без набора — это список наборов: прежний выбор не остаётся на экране
    st.list = r.page === 'sets' && !r.set;
    if (st.list) { st.sel = null; }
    applyPage(r.page);
    if (r.page === 'stats') { setPeriod(r.period, true); }
    if (r.page === 'sets' && r.set) {
      if (r.set !== st.sel) { select(r.set, true); }
      // вкладка из адреса: неизвестная уже сведена к обзору в routeOf
      if (r.tab !== st.tab) { setTab(r.tab); }
    }
  }

  window.addEventListener('hashchange', route);

  var resizeTimer = 0;
  window.addEventListener('resize', function () {
    applyRail();
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      if (st.page === 'stats' && st.st.data) { renderSeries(st.st.data.series || []); }
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

  /* ---------- оформление: тема и схема ----------
     Две независимые оси, как в theme.js: data-theme (вид) и data-palette (цвета).
     У каждой темы свои схемы, поэтому в меню показана группа своей семьи, а
     выбор запоминается отдельно — вернулся к теме, вернулись её цвета. */

  var SCHEMES = {
    'default': { dark: 1, light: 1, nord: 1, gruvbox: 1 },
    win98: { 'w98': 1, 'w98-cool': 1, 'w98-warm': 1, 'w98-hc': 1 }
  };
  var FIRST = { 'default': 'dark', win98: 'w98' };

  function schemeKey(theme) { return theme === 'win98' ? 'mcprig.palette.win98' : 'mcprig.palette'; }

  function storeLocal(key, val) {
    try { localStorage.setItem(key, val); } catch (e) { /* приватный режим */ }
  }

  function readLocal(key) {
    try { return localStorage.getItem(key) || ''; } catch (e) { return ''; }
  }

  // сам атрибуты ставит theme.js до первой отрисовки; меню только меняет их
  function applyThemeMenu() {
    var root = document.documentElement;
    var theme = root.getAttribute('data-theme') || 'default';
    var palette = root.getAttribute('data-palette') || 'dark';
    [].forEach.call(document.querySelectorAll('[data-theme-opt]'), function (b) {
      b.setAttribute('aria-checked', String(b.dataset.themeOpt === theme));
    });
    [].forEach.call(document.querySelectorAll('[data-palette-opt]'), function (b) {
      b.setAttribute('aria-checked', String(b.dataset.paletteOpt === palette));
    });
    [].forEach.call(document.querySelectorAll('[data-family]'), function (g) {
      g.hidden = g.dataset.family !== theme;
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
    var btn = e.target.closest('[data-theme-opt], [data-palette-opt]');
    if (!btn) { return; }
    var root = document.documentElement;
    if (btn.dataset.themeOpt) {
      var theme = btn.dataset.themeOpt;
      root.setAttribute('data-theme', theme);
      storeLocal('mcprig.theme', theme);
      // у новой темы своя семья схем: берём её прошлый выбор или первую из семьи
      var kept = readLocal(schemeKey(theme));
      var palette = SCHEMES[theme] && SCHEMES[theme][kept] ? kept : FIRST[theme];
      root.setAttribute('data-palette', palette);
      storeLocal(schemeKey(theme), palette);
    } else {
      root.setAttribute('data-palette', btn.dataset.paletteOpt);
      storeLocal(schemeKey(root.getAttribute('data-theme') || 'default'), btn.dataset.paletteOpt);
    }
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
    var back = label || btn.innerHTML;
    var done = function (ok) {
      btn.textContent = ok ? t('head.copied') : t('head.copyFail');
      clearTimeout(copyTimer);
      // кнопки здесь иконочные: текст возвращается разметкой, а не строкой
      copyTimer = setTimeout(function () { btn.innerHTML = back; }, 1200);
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

  // токена в панели нет: копируется префикс, без многоточия — так его и вставляют
  $('key-copy').addEventListener('click', function () {
    var set = current(), text = (set && set.keyPrefix) || '';
    if (!text) { return; }
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

  $('set-del').addEventListener('click', delSet);

  /* ---------- импорт и экспорт ---------- */

  $('io').addEventListener('click', openIo);
  $('empty-io').addEventListener('click', openIo);
  $('io-x').addEventListener('click', closeIo);
  $('io-close').addEventListener('click', closeIo);
  $('io-add').addEventListener('click', ioImport);
  $('io-text').addEventListener('input', ioPreview);

  /* ---------- промт для агента ---------- */

  $('prompt-open').addEventListener('click', openPrompt);
  $('prompt-x').addEventListener('click', closePrompt);
  $('prompt-close').addEventListener('click', closePrompt);
  $('prompt-build').addEventListener('click', buildPrompt);
  $('prompt-copy').addEventListener('click', function () {
    if (promptText) { copyText(promptText, this, t('prompt.copy')); }
  });
  $('prompt-dl').addEventListener('click', function () {
    var set = current();
    if (promptText && set) { download('mcprig-prompt-' + set.id + '.md', promptText, 'text/markdown'); }
  });

  // файл — только источник текста: предпросмотр и отправка идут тем же путём
  $('io-file').addEventListener('change', function () {
    var file = this.files && this.files[0];
    if (!file) { return; }
    var reader = new FileReader();
    reader.onload = function () {
      $('io-text').value = String(reader.result || '');
      ioPreview();
    };
    reader.onerror = function () { ioMsg(t('io.fail', { msg: file.name })); };
    reader.readAsText(file);
  });

  $('io-reg-dl').addEventListener('click', function () {
    if (ioData) { download('mcprig-registry.json', ioData); }
  });

  $('io-reg-copy').addEventListener('click', function () {
    if (ioData) { copyText(JSON.stringify(ioData, null, 2), this); }
  });

  $('io-set-dl').addEventListener('click', function () {
    var id = $('io-set').value;
    if (!id) { return; }
    get('/api/export?set=' + encodeURIComponent(id)).then(function (d) {
      if (!d) { throw new Error(t('io.noData')); }
      download('mcprig-' + id + '.json', d);
    }).catch(function (err) { ioMsg(err.message); });
  });

  $('log-refresh').addEventListener('click', function () { loadLog(); });
  $('errbar-close').addEventListener('click', function () { show($('errbar'), false); });

  /* ---------- список серверов набора и реестра ---------- */

  $('set-srv-body').addEventListener('click', function (e) {
    var check = e.target.closest('[data-check]');
    if (check) { probeServer(check.dataset.check, check); return; }
    var logBtn = e.target.closest('[data-log]');
    if (logBtn) { openSrvLog(logBtn.dataset.log); return; }
    var toolsBtn = e.target.closest('[data-tools]');
    if (toolsBtn) { openTools(toolsBtn.dataset.tools); return; }
    var edit = e.target.closest('[data-edit]');
    if (edit) { openSrv(edit.dataset.edit); return; }
    var del = e.target.closest('[data-del]');
    if (del) {
      var set = current();
      if (!set) { return; }
      saveSetServers(del, (set.servers || []).filter(function (n) { return n !== del.dataset.del; }));
    }
  });

  $('srv-attach-add').addEventListener('click', function () {
    var set = current();
    var name = $('srv-attach').value;
    if (!set || !name) { return; }
    saveSetServers(this, (set.servers || []).concat([name]));
  });

  $('srvlog-close').addEventListener('click', closeSrvLog);
  $('srvlog-refresh').addEventListener('click', loadSrvLog);
  $('tools-refresh').addEventListener('click', loadTools);
  $('tools-server').addEventListener('change', function () {
    st.toolsFilter = this.value;
    renderTools();
  });
  $('lock-body').addEventListener('click', function (e) {
    var btn = e.target.closest('[data-approve]');
    if (!btn) { return; }
    var tr = btn.closest('tr');
    approveTool(tr.dataset.server, btn.dataset.approve, btn);
  });
  $('calls-more').addEventListener('click', loadMoreCalls);
  $('calls-resume').addEventListener('click', resumeCalls);

  $('servers').addEventListener('click', function (e) {
    var check = e.target.closest('[data-check]');
    if (check) { probeServer(check.dataset.check, check); return; }
    var auth = e.target.closest('[data-authorize]');
    if (auth && auth.dataset.authorize) { startAuth(auth.dataset.authorize); return; }
    var logBtn = e.target.closest('[data-log]');
    if (logBtn) { openSrvLog(logBtn.dataset.log, logBtn.dataset.set); return; }
    var edit = e.target.closest('[data-edit]');
    if (edit) { openSrv(edit.dataset.edit); return; }
    var del = e.target.closest('[data-del]');
    if (del) { delServerRow(del.dataset.del, del); }
  });

  $('srv-new').addEventListener('click', function () { openSrv(''); });

  $('envform').addEventListener('submit', saveEnv);
  $('env').addEventListener('click', function (e) {
    var del = e.target.closest('[data-envdel]');
    if (del) { delEnv(del.dataset.envdel, del); }
  });

  $('auth-close').addEventListener('click', closeAuth);
  $('auth-done').addEventListener('click', closeAuth);
  $('auth-dlg').addEventListener('close', function () {
    authFlow = null;
    clearTimeout(authPoll);
  });
  $('auth-open').addEventListener('click', function () {
    var url = $('auth-link').value;
    if (url) { window.open(url, '_blank', 'noopener'); }
  });
  $('auth-copy').addEventListener('click', function () {
    var btn = this;
    navigator.clipboard.writeText($('auth-link').value).then(function () {
      var was = t('auth.copy');
      btn.textContent = t('auth.copied');
      setTimeout(function () { btn.textContent = was; }, 1500);
    });
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
      return loadState().then(function () { if (set && set.id) { select(set.id, true); } });
    }).catch(function (ex) {
      err.textContent = ex.message;
      err.hidden = false;
    });
  });

  /* ---------- ключи ---------- */

  $('new-key-copy').addEventListener('click', function () {
    copyText($('new-key').textContent, this);
  });

  // Подпись уходит в теле выдачи: ключ получает её тем же запросом, и набор
  // перезагружается один раз, а не дважды.
  $('key-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var set = current();
    if (!set || st.busy) { return; }
    var label = $('key-label').value.trim();
    var profile = $('key-profile').value;
    var body = {};
    if (profile) { body.profile = profile; }
    if (label) { body.label = label; }
    var btn = this.querySelector('button[type="submit"]');
    st.busy = true;
    btn.disabled = true;
    show($('keys-warn'), false);
    send('POST', '/api/sets/' + encodeURIComponent(set.id) + '/keys', body)
      .then(function (r) {
        $('key-label').value = '';
        $('new-key').textContent = (r && r.token) || '';
        show($('new-key-box'), !!(r && r.token));
        if (r && r.restartError) { fail(t('keys.restartWarn', { err: r.restartError })); }
        return loadState().then(function () { renderKeys(current() || set); });
      })
      .catch(function (err) {
        // Отказ демона (400 на неизвестный профиль) — ответ формы, а не сбой
        // связи: показываем его текст здесь, диалог не закрываем.
        $('keys-warn').textContent = err.message;
        show($('keys-warn'), true);
      })
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
    show($('keys-warn'), false);
    send('DELETE', '/api/sets/' + encodeURIComponent(set.id) + '/keys/' +
      encodeURIComponent(revoke.dataset.revoke)).then(function (r) {
      return loadState().then(function () {
        var now = current() || set;
        renderKeys(now);
        // Отзыв активного ключа останавливает набор: без токена он не слушает.
        // Молчать об этом нельзя — панель выглядела бы просто «остановленной».
        if (r && r.wasActive && !(now.runtime && now.runtime.running)) {
          $('keys-warn').textContent = t('keys.revokedStopped');
          show($('keys-warn'), true);
        }
      });
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
    save.hidden = e.target.value.trim() === ((stored && stored.label) || '');
  });

  /* ---------- цикл обновления ---------- */

  function every(ms, fn) { setInterval(function () { if (!document.hidden) { fn(); } }, ms); }

  try { var rp = localStorage.getItem('mcprig.rail'); st.railPref = rp === 'compact' || rp === 'full' ? rp : ''; } catch (e) { /* приватный режим */ }
  applyStatic();
  setTitle();
  applyRail();
  applyThemeMenu();
  // первый кадр показывает то, что назвал адрес: страницу и вкладку набора.
  // Иначе ссылка на #/set/x/logs мигала бы чужим обзором до первого ответа
  applyPage(st.page);
  applyTab(st.sel ? st.tab : null);
  if (st.page === 'stats') { setPeriod(st.st.period, true); }
  loadState().then(function () {
    every(5000, loadState);
    every(5000, function () {
      if (!st.sel) { return; }
      loadHealth();
      // пока листают историю, страница не перезаписывается первой
      if (!st.callsMore.paged) { loadCalls(); }
    });
    // лог читается, только когда открыта вкладка лога: у скрытой незачем
    every(10000, function () {
      var set = current();
      if (set && set.runtime && set.runtime.running && st.tab === 'logs') { loadLog(); }
    });
    every(15000, function () {
      if (st.page === 'stats') { loadStats(); }
      if (st.page === 'host') { refreshHost(); }
    });
    every(30000, loadEnv);
  });
})();