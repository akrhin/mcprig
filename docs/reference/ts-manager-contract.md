# Контракт TS-эталона

Референс: `/tmp/opencode/mm/mcp-manager` (распакован из
`lazy-mcp/mcp-manager-2026-11-05.tar.gz`), форк `lazy-mcp@2.10.4`.
Строки ниже — по файлам относительно `src/manager/`.

Документ фиксирует **поведение**, которое Go-версия обязана сохранить, и
**дефекты**, которые она обязана исправить (последний раздел).

## HTTP API

Тело не JSON → `400 {error:"ожидался JSON"}` (`server.ts:179`); тело > 1 МиБ →
`400` (`:168`). Неизвестный маршрут → `404 {error:"not found"}` (`:595`).
SSE, CORS, OPTIONS нет. Ошибки: `HttpError.status`, иначе `400`.

| Метод | Путь | Тело/query | Ответ |
|---|---|---|---|
| GET | `/healthz` | — | `200 {"status":"ok","sets":N}`, N — наборов в реестре; `503 {"status":"degraded","dead":["id",…]}` при мёртвом autostart-наборе; до auth |
| GET | `/login` | — | HTML; `404`, если парольный режим выключен |
| POST | `/api/login` | `{user,password}` | `{ok,user}` + cookie `mcp_session=<tok>.<sig>`, `HttpOnly; SameSite=Lax; Max-Age=43200`; `401` |
| POST | `/api/logout` | — | `{ok:true}`, cookie с `Max-Age=0` |
| GET | `/` , `/ui` | — | панель: `web/` из `go:embed` (index.html, app.css, app.js, favicon, `mock/`), CSP `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`; `/ui` → `302 /` |
| GET | `/api/state` | — | `{servers, sets, firstPort:9100}`; токены маскируются в `prefix` (8 символов) |
| POST | `/api/import` | `{mcpServers:{name:{command\|string\|[],args,url,env,headers,cwd,description,tags,enabled}}}` | `{added[],skipped[{name,reason}]}` |
| GET | `/api/export/registry` | — | сид реестра без токенов |
| GET | `/api/export` | `?set=<id>` | `{mcpServers:{…}}` с `${VAR}`; `400` без `set`, `404` |
| GET | `/api/env` | — | `{remoteError?, vars:[{name,set,source:'file'\|'openbao'\|'env'}]}` |
| PUT | `/api/env/:NAME` | `{value, store?}` | `{name,set:true,source,restarted[]}` |
| DELETE | `/api/env/:NAME` | `?store=local\|file\|openbao` | `{name,removed,source,restarted[]}`; `source` — `openbao`, `file` или пусто, когда удалять было нечего |
| POST | `/api/servers/:name/probe` | — | `ProbeResult{ok,tools[],error?,ms}` всегда `200` |
| POST | `/api/browse` | `{server}` \| `{entry}` | `BrowseView`; `502` при ошибке connect |
| GET/DELETE | `/api/browse/:id` | — | `BrowseView` / `{closed:true}` |
| POST | `/api/browse/:id/{tool,resource,prompt}` | `{name,args?}` \| `{uri}` | `{result}` |
| GET/POST | `/api/servers` | `ServerEntry` | `ServerEntry[]` / `201 ServerEntry` |
| PUT/DELETE | `/api/servers/:name` | частичный `ServerEntry` | `{...updated,restarted[]}` / `{ok:true,stopped[]}` |
| POST | `/api/sets/:id/keys` | — | `{token, key, set}`; работающий набор перезапускается |
| DELETE | `/api/sets/:id/keys/:keyId` | — | `{ok,wasActive,key,set}`; активный ключ гасит набор |
| GET | `/api/sets/:id/servers` | — | `{running, servers:[{name,healthy:boolean\|null,error?}]}` |
| GET | `/api/sets/:id/servers/:name/logs` | `?lines` (200, max 5000) | `{server,logs[]}` |
| GET/POST | `/api/sets` | `{name,servers[],port?,token?,description?,autostart?,open?}` | `SetEntry[]` / `201`; порт и токен выдаются автоматически |
| PUT/DELETE | `/api/sets/:id` | частичный | `200` / `{ok:true}` (сначала stop) |
| GET | `/api/sets/:id/logs` | `?lines` (200, max 5000) | `{logs[]}` |
| GET | `/api/calls` | `?set=<id>&limit=<n>` (50, max 500) | `{set?, calls:[{time,set?,client,server,tool,ms,failed,error?}], counters:[{set?,client,server,tool,calls,failures,totalMs,p50ms,p95ms}], unreachable?:[…]}`, `404` на неизвестный набор |
| POST | `/api/sets/:id/{start,stop,restart}` | — | `RuntimeStatus`; иначе `405` |

В `PUT` и `DELETE /api/env` параметр `store` принимает `local` (алиас из
TS-эталона) и `file`; в ответе источник всегда `file`. `openbao` — хранилище
OpenBao.

UI: панель — статические файлы из `go:embed`, `GET /api/state` и `GET /api/calls` опрашиваются каждые 5 с; inline-JS нет.

`/stats` набора (не панели): `GET /stats` с тем же bearer, что `/mcp`;
снимок `{set,counters[],tail[]}` с `p50ms`/`p95ms`. У открытого набора без ключа
снимок редактированный: `client` = `"(аноним)"`, тексты ошибок пусты; счётчики,
латентности и хвост остаются. Полного снимка анонимно не отдаём.

## Модель данных

```
TransportKind = "stdio" | "http"
ServerEntry   {name, description, transport, command?: []string, cwd?, env?: map, url?, headers?, tags?, enabled?}
SetKey        {id, token, createdAt, revokedAt?, note?}
SetEntry      {id, name, description?, servers: []string, port, keys: []SetKey, token, autostart?, open?}
RegistryData  {version:1, servers, sets, nextPort}
```

- `NAME_PATTERN = /^[a-z0-9][a-z0-9_-]{0,63}$/` (`domain/validation.ts:9`).
- transport: явное поле главнее, иначе `url` → http (`:15`).
- stdio требует `command[]`, http требует `url`; `cwd` только для stdio; `env`/`headers` — только строки.
- `SetEntry.id = slugify(id ?? name)`; набор без серверов не валиден; все имена обязаны быть в реестре.
- Ключи: ровно один активный (`revokedAt` пуст), `token` = зеркало активного,
  `newKeyToken()` = 48 hex (`randomBytes(24)`), `newKeyId()` = 12 hex. Старое поле
  `token` мигрирует в ключ `legacy`.

## Диск (`MANAGER_DATA_DIR`, деф. `/data`)

| Файл | Формат | Права |
|---|---|---|
| `registry.json` | `RegistryData`, JSON 2 пробела, запись `tmp`+rename | 0600 |
| `registry.json.corrupt-<ts>` | битый файл переименовывается, старт с пустого | 0600 |
| `registry.seed.json` (`MANAGER_SEED_FILE`) | применяется только к пустому реестру | — |
| `sets/<id>/servers.json` | конфиг lazy-mcp, `${VAR}` **не раскрыты** | 0600 |
| `sets/<id>/lazy-mcp.log` | stdout+stderr, ротация в `.log.1` при 5 МиБ | — |
| `env.json` | плоский `{"NAME":"value"}`, только строки | 0600 |
| `$TRAEFIK_DIR/mcp-<id>.yml` | роут | 0644 |

## Runtime набора

- `spawn(process.execPath, [cliPath, "--config", file])`, без shell; `stdio ['ignore', fd, fd]`.
- Готовность: TCP-connect `127.0.0.1:<port>` каждые 250 мс, бюджет 15 000 мс;
  провал → `SIGKILL` + ошибка «порт не слушает».
- Падение → авто-рестарт до 5 раз, backoff `min(1000·2^(n-1), 30 000)`.
- `stop`: `SIGTERM`, ожидание 5 000 мс, `SIGKILL`; `stopAll` — 3 000 мс на процесс.
- Набор без активного ключа (и не `open`) стартовать отказывается.
- Открытый набор (`allowAnonymous`): пустой `authToken`, старт без ключа. Запрос
  без `Authorization` проходит, но **любой предъявленный `Authorization` получает
  `401`**: значение сравнивается с пустым токеном, и сравнение всегда ложно. Это
  не уязвимость, а свойство открытого набора — клиент, который шлёт заголовок
  всегда, в него не пройдёт и должен либо получить ключ, либо не слать заголовок.
  `/stats` открытого набора доступен анонимно в редакции: без меток клиентов и
  текстов ошибок апстрима.
- Конфиг набора: `transport{type:"http",host:"0.0.0.0",port,path:"/mcp",authToken,allowedHosts}`,
  `healthMonitor{interval:60000,timeout:120000}`, `requestTimeout:120000`.
- Env ребёнка — whitelist (`PATH, HOME, LANG, LC_*, TZ, TMPDIR, XDG_*, SSL_*, NODE_EXTRA_CA_CERTS`)
  плюс только те `${VAR}`, на которые ссылаются серверы этого набора.
- Probe: `initialize` + `tools/list`, бюджет 60 000 мс, ошибка ≤ 800 символов.
- Health: `list_servers` по HTTP набору с bearer, TTL кеша 15 000 мс, single-flight;
  сетевая ошибка → `healthy: null` (не `false`).

## Секреты и auth

- Цепочка источников: OpenBao KV v2 (`<mount>/data/<prefix>/<NAME>`, поле `value`,
  деф. `mount=secret, prefix=mcp`) → `env.json` → окружение процесса.
  Чтение — первый, где значение есть; запись — первый доступный или явный `store`.
- Пароль: `scrypt$N$r$p$salt$hash`, `N=16384, r=8, p=1, keylen=32`, сверка `timingSafeEqual`.
- Сессия: cookie `<token>.<HMAC-SHA256>`, TTL 12 ч, в памяти (рестарт инвалидирует).
- Режимы: `none | proxy | password | proxy+password` по наличию
  `MANAGER_PROXY_SECRET` и `MANAGER_PASSWORD`/`MANAGER_PASSWORD_HASH`.
- `MANAGER_ALLOWED_HOSTS` — проверка `Host` без порта, иначе `403`.
- Proxy-режим: `Remote-User` (`MANAGER_USER_HEADER`) + `x-mcp-proxy-secret`.
- Env: `MANAGER_{DATA_DIR,HOST,PORT,USER,USER_HEADER,ALLOWED_HOSTS,SEED_FILE,PROXY_SECRET,PASSWORD,PASSWORD_HASH}`,
  `LAZY_MCP_CLI`, `BAO_*`/`OPENBAO_*`, `TRAEFIK_DIR`, `TRAEFIK_TARGET_HOST`, `MCP_BIND_IP`.

## Что Go-версия обязана исправить (не переносить)

Блокеры ревизии 2026-11-04, каждый — требование, а не наследство:

1. **Auth fail-closed.** Без `MANAGER_PASSWORD*`/`MANAGER_PROXY_SECRET` — либо отказ
   старта, либо явный `MANAGER_AUTH=off` с предупреждением. Сейчас API открыт
   (unauthenticated RCE: `command[]`, rw-монт `/traefik`, токены всех наборов).
2. **CSRF/rebinding.** Проверять `Content-Type` и `Origin` в теле-хендлере, `Host` —
   всегда (в upstream это есть в `src/http-server.ts:336-371`, в менеджере нет).
3. **Никакого inline-JS в UI.** Stored XSS через `esc()` в `onclick`
   (`ui.ts:599,606,921`) — шаблоны `html/template` с автоэкранированием, без строковой сборки.
4. **Изоляция env набора.** В ребёнок идут только `${VAR}` его серверов; `env.json`
   целиком не передаётся никогда (`runtime.ts:120-123`).
5. **Токены не уходят в клиент.** `/api/sets` и `POST/PUT /api/sets` сейчас отдают
   сырые `keys[]`/`token`; клиент и `id`, и `token` задавать не должен.
6. **Коды ответов.** `404` на отсутствующий ресурс, `405` на неверный метод,
   `409` на конфликт; `5xx` логируются.
7. **Порт 9100–9199.** Валидация сейчас 1024–65535 — набор на 9200 поднимается,
   но недоступен.
8. **Супервизия.** `stop`/`exit` не должны оставлять висячий роут; `stopAll` —
   с `SIGKILL`-фолбэком; `pid-lock` используется, а не лежит.
9. **Health и логи.** `/healthz` отдаёт `200 {"status":"ok","sets":N}` (N — сколько
   наборов в реестре), когда все autostart-наборы живы или их нет, и
   `503 {"status":"degraded","dead":["id",…]}` когда autostart-набор задан, но
   процесса нет; логи читаются хвостом файла
   набора, фильтр по точному имени сервера, не подстрокой.
10. **Атомарность.** `fsync`/`.bak` перед перезаписью реестра; `JSON.parse` с
    восстановлением; ошибки записи не глотаются.
