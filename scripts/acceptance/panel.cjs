// Приёмка панели: 38 шагов от чистого каталога данных — рейл, темы, навигация,
// наборы, серверы, ключи, инструменты, статистика, журнал, переменные, импорт.
//
// Запуск (нужен playwright; панель поднимается на чистом каталоге, без пароля):
//   MCPRIG_DATA_DIR=$(mktemp -d) MCPRIG_AUTH=off MCPRIG_PORT=9131 ./mcprig panel &
//   ACC_BASE=http://127.0.0.1:9131 ACC_DAP=/path/to/stdio-mcp npm i -g playwright && node panel.cjs
//
// ACC_DAP — любой stdio MCP-сервер: по нему проверяются привязка, инструменты и журнал.
// Без ACC_DAP эти шаги пропускаются (в CI браузер есть, stdio-сервера нет), остальные
// выполняются, и итог failed считается по выполненным.
const { chromium } = require("playwright");
const BASE = process.env.ACC_BASE || "http://127.0.0.1:9131";
const DAP = process.env.ACC_DAP || "";

const results = [];
let page, dialogMode = "accept", dialogs = [];

function ok(name, extra) { results.push({ name, pass: true, extra: extra === undefined ? "" : String(extra) }); }
function bad(name, why) { results.push({ name, pass: false, why: String(why).replace(/\s+/g, " ").slice(0, 300) }); }
let lastStep = "-";
async function step(name, fn) {
  lastStep = name;
  try {
    const v = await fn();
    if (v === false) { bad(name, "assert"); } else { ok(name, v); }
  } catch (e) { bad(name, e.message); }
}
const skipped = [];
function skip(name) { skipped.push(name); console.log(`skip ${name} (ACC_DAP не задан)`); }
// Шаги с привязанным stdio-сервером: без ACC_DAP их нечем проверить.
const stepDap = (name, fn) => (DAP ? step(name, fn) : skip(name));
const api = async (path, opts) => page.evaluate(async ([p, o]) => {
  const r = await fetch(p, Object.assign({ headers: { Origin: location.origin, "Content-Type": "application/json" } }, o || {}));
  const t = await r.text();
  try { return { status: r.status, body: JSON.parse(t) }; } catch (e) { return { status: r.status, body: t }; }
}, [path, opts || null]);
const state = async () => (await api("/api/state")).body;
const setOf = async id => ((await state()).sets || []).find(s => s.id === id);
async function go(hash) {
  const url = `${BASE}/${hash}`;
  const same = page.url() === url;
  await page.goto(url, { waitUntil: "networkidle" });
  if (same) { await page.reload({ waitUntil: "networkidle" }); } // goto на тот же адрес не перезагружает: маршрут не отработает
  // ждём, пока маршрут отработает и страница отрисуется: иначе следующий шаг
  // проверяет ещё пустой DOM и падает «элемента нет»
  // Таймаут здесь — провал шага, а не повод идти дальше: иначе следующий шаг
  // падает «элемента нет» на недорисованной странице и прячет настоящую причину.
  await page.waitForFunction((h) => location.hash === h, hash, { timeout: 8000 });
  if (hash.indexOf("#/set/") === 0) {
    await page.waitForFunction(() => document.querySelectorAll("#tabs button[data-tab]").length >= 6, null, { timeout: 8000 });
  }
  await page.waitForTimeout(400);
}
async function waitFor(fn, ms = 15000, gap = 300) {
  const end = Date.now() + ms;
  while (Date.now() < end) { const v = await fn(); if (v) return v; await page.waitForTimeout(gap); }
  return null;
}

(async () => {
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, permissions: ["clipboard-read", "clipboard-write"] });
  page = await context.newPage();
  const consoleErrors = [], httpErrors = [];
  const toolsLog = [];
page.on("response", async (r) => {
  if (!/\/tools(\?|$)/.test(r.url())) return;
  let n = -1;
  try { const j = await r.json(); n = Array.isArray(j.tools) ? j.tools.length : -1; } catch (e) { n = -2; }
  toolsLog.push({ t: Date.now(), status: r.status(), n });
});
page.on("pageerror", e => consoleErrors.push("pageerror@" + lastStep + ": " + (e.stack || e.message).split("\n").slice(0, 4).join(" / ")));
  page.on("console", m => { if (m.type() === "error" && !/Failed to load resource/.test(m.text())) consoleErrors.push(m.text()); });
  page.on("response", r => { if (r.status() >= 400) httpErrors.push(r.status() + " " + r.request().method() + " " + new URL(r.url()).pathname + new URL(r.url()).search); });
  page.on("dialog", d => { dialogs.push(d.message()); if (dialogMode === "accept") { d.accept(); } else { d.dismiss(); } });

  await page.goto(`${BASE}/#/`, { waitUntil: "networkidle" });
  await page.waitForTimeout(400);

  /* ---------- рейл и оболочка ---------- */
  await step("R1 рейл складывается кнопкой", async () => {
    const probe = () => page.evaluate(() => [document.documentElement.classList.contains("rail-compact"), localStorage.getItem("mcprig.rail"), document.querySelector("#rail-toggle").getAttribute("aria-pressed")].join("|"));
    const before = await probe();
    await page.click("#rail-toggle");
    await page.waitForTimeout(300);
    const after = await probe();
    await page.click("#rail-toggle");
    await page.waitForTimeout(300);
    const back = await probe();
    if (before === after) throw new Error("состояние не изменилось: " + before);
    if (back.split("|")[0] !== before.split("|")[0]) throw new Error(`не вернулось обратно: ${before} → ${back}`);
    return `${before} → ${after} → ${back}`;
  });

  await step("R2 пустое состояние: кнопка «создать набор» открывает форму", async () => {
    await go("#/");
    await page.click("#empty-add");
    await page.waitForTimeout(300);
    if (!(await page.isVisible("#add-name"))) throw new Error("форма не открылась");
    await page.click("#add-cancel").catch(() => page.click("#add-toggle"));
    await page.waitForTimeout(300);
  });

  await step("R3 отмена формы не создаёт набор", async () => {
    const s = await state();
    if ((s.sets || []).length) throw new Error("набор появился");
  });

  await step("R4 пустое имя отклоняется", async () => {
    await page.click("#add-toggle");
    await page.fill("#add-name", "");
    await page.fill("#add-port", "");
    await page.click("#addform button[type=submit]");
    await page.waitForTimeout(700);
    const s = await state();
    const err = (await page.textContent("#add-err").catch(() => "")) || "";
    const open = await page.isVisible("#add-name");
    if ((s.sets || []).length) throw new Error("набор создан без имени");
    if (!err.trim() && !open) throw new Error("и ошибки нет, и форма закрыта");
    return err.trim().slice(0, 60) || "форма осталась открытой";
  });

  await step("R5 порт вне диапазона отклоняется", async () => {
    await page.fill("#add-name", "zz");
    await page.fill("#add-port", "22");
    await page.click("#addform button[type=submit]");
    await page.waitForTimeout(600);
    const s = await state();
    if ((s.sets || []).find(x => x.id === "zz")) throw new Error("создан с портом 22");
    return ((await page.textContent("#add-err").catch(() => "")) || "").trim().slice(0, 60);
  });

  let alphaPort = 0;
  await step("R6 создание набора, адрес и вкладки", async () => {
    await page.fill("#add-name", "alpha");
    await page.fill("#add-port", "");
    await page.click("#addform button[type=submit]");
    const a = await waitFor(async () => (await setOf("alpha")));
    if (!a) throw new Error("набора нет");
    alphaPort = a.port;
    const hash = await waitFor(async () => { const h = await page.evaluate(() => location.hash); return h === "#/set/alpha" ? h : null; }, 5000);
    const tabs = await page.locator("#tabs .tab").count();
    if (!hash) throw new Error("адрес " + (await page.evaluate(() => location.hash)));
    if (tabs !== 6) throw new Error("вкладок " + tabs);
    return `порт ${alphaPort}`;
  });

  await step("R7 дубль имени отклоняется", async () => {
    await go("#/");
    await page.click("#add-toggle");
    await page.fill("#add-name", "alpha");
    await page.fill("#add-port", "9190");
    await page.click("#addform button[type=submit]");
    await page.waitForTimeout(800);
    const s = await state();
    if ((s.sets || []).filter(x => x.id === "alpha").length !== 1) throw new Error("создан второй alpha");
    return ((await page.textContent("#add-err").catch(() => "")) || "").trim().slice(0, 60);
  });

  await step("R8 занятый порт отклоняется", async () => {
    await page.fill("#add-name", "beta");
    await page.fill("#add-port", String(alphaPort));
    await page.click("#addform button[type=submit]");
    await page.waitForTimeout(800);
    if (await setOf("beta")) throw new Error("beta создан на занятом порту");
    return ((await page.textContent("#add-err").catch(() => "")) || "").trim().slice(0, 60);
  });

  await step("R9 свободный порт принимается, второй набор виден в рейле", async () => {
    await page.fill("#add-port", "9190");
    await page.click("#addform button[type=submit]");
    const b = await waitFor(async () => (await setOf("beta")));
    if (!b) throw new Error("beta не создан");
    const rail = await waitFor(async () => (await page.locator('#rail-sets [data-id="beta"]').count()) > 0, 8000);
    if (!rail) throw new Error("в рейле нет beta");
    return "порт " + b.port;
  });

  await step("R10 клик по набору в рейле открывает его с любой страницы", async () => {
    await go("#/stats/today");
    await page.click('#rail-sets [data-id="alpha"]');
    await page.waitForTimeout(500);
    const h = await page.evaluate(() => location.hash);
    if (h !== "#/set/alpha") throw new Error("адрес " + h);
    await go("#/");
    await page.click('#rail-sets [data-id="beta"]');
    await page.waitForTimeout(400);
    const h2 = await page.evaluate(() => location.hash);
    if (h2 !== "#/set/beta") throw new Error("с обзора: " + h2);
  });

  await step("S1 список наборов открывает набор", async () => {
    await go("#/sets");
    const rows = await page.locator("#sets-table tbody tr").count();
    if (rows !== 2) throw new Error("строк " + rows);
    await page.click('#sets-table tbody a[href="#/set/alpha"]');
    await page.waitForTimeout(500);
    const h = await page.evaluate(() => location.hash);
    if (!h.startsWith("#/set/alpha")) throw new Error("адрес " + h);
  });

  await step("S2 навигация по страницам и назад", async () => {
    const seen = [];
    for (const [label, slug] of [["Overview", "host"], ["Sets", "sets"], ["Statistics", "stats"], ["All servers", "servers"], ["Variables", "vars"]]) {
      await page.click(`a[data-page="${slug}"]`);
      await page.waitForTimeout(350);
      const h = await page.evaluate(() => location.hash);
      if (h.indexOf(slug === "host" ? "#/" : "#/" + slug) !== 0) throw new Error(label + " → " + h);
      seen.push(h);
    }
    await page.goBack();
    await page.waitForTimeout(400);
    const back = await page.evaluate(() => location.hash);
    if (back !== "#/servers") throw new Error("назад → " + back);
    return seen.join(" ");
  });

  await step("S3 неизвестный адрес показывает список наборов", async () => {
    await go("#/nonsense");
    const list = await page.isVisible("#sets-list");
    const host = await page.isVisible("#page-host");
    if (!list || host) throw new Error(`list=${list} host=${host}`);
  });

  await step("T1 копирование эндпоинта в буфер", async () => {
    await go("#/set/alpha");
    await page.click("#copy");
    await page.waitForTimeout(400);
    const clip = await page.evaluate(() => navigator.clipboard.readText().catch(() => ""));
    if (!clip || clip.indexOf("/mcp") < 0) throw new Error("в буфере " + JSON.stringify(clip));
    return clip;
  });

  await step("T2 смена языка туда и обратно", async () => {
    await page.selectOption("#lang", "ru");
    await page.waitForTimeout(500);
    const ru = await page.textContent("#host-h").catch(() => "");
    await page.selectOption("#lang", "en");
    await page.waitForTimeout(400);
    const en = await page.textContent("#host-h").catch(() => "");
    if (!/[А-Яа-я]/.test(ru || "")) throw new Error("RU не применился: " + ru);
    if (/[А-Яа-я]/.test(en || "")) throw new Error("EN не вернулся: " + en);
  });

  await step("T3 все восемь схем применяются и дают разный фон", async () => {
    const bgs = {};
    for (const [theme, pal] of [["default", "dark"], ["default", "light"], ["default", "nord"], ["default", "gruvbox"],
      ["win98", "w98"], ["win98", "w98-cool"], ["win98", "w98-warm"], ["win98", "w98-hc"]]) {
      await page.evaluate(([t, p]) => {
        localStorage.setItem("mcprig.theme", t);
        localStorage.setItem("mcprig.palette", p);
        localStorage.setItem("mcprig.palette.win98", p);
      }, [theme, pal]);
      await page.reload({ waitUntil: "networkidle" });
      await page.waitForTimeout(300);
      const m = await page.evaluate(() => [document.documentElement.dataset.theme, document.documentElement.dataset.palette, getComputedStyle(document.body).backgroundColor]);
      if (m[0] !== theme || m[1] !== pal) throw new Error(`${pal}: ${m[0]}/${m[1]}`);
      bgs[pal] = m[2];
    }
    const uniq = new Set(Object.values(bgs));
    if (uniq.size < 5) throw new Error("фоны почти одинаковы: " + JSON.stringify(bgs));
    await page.evaluate(() => {
      localStorage.setItem("mcprig.theme", "default");
      localStorage.setItem("mcprig.palette", "dark");
    });
    await page.reload({ waitUntil: "networkidle" });
    return uniq.size + " разных фонов";
  });

  await step("T4 меню темы открывается и закрывается по Esc", async () => {
    await page.click("#theme-btn");
    const open = await page.isVisible("#theme-menu");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(250);
    const closed = !(await page.isVisible("#theme-menu"));
    if (!open || !closed) throw new Error(`open=${open} closed=${closed}`);
  });

  /* ---------- серверы ---------- */
  await stepDap("U1 создание сервера: пустое имя отклоняется", async () => {
    await go("#/servers");
    await page.click("#srv-new");
    await page.waitForTimeout(300);
    await page.fill("#sf-name", "");
    await page.fill("#sf-command", DAP);
    await page.click("#srv-save");
    await page.waitForTimeout(600);
    const dlgOpen = await page.isVisible("#srv-dlg");
    const err = (await page.textContent("#srv-err").catch(() => "")) || "";
    if (!dlgOpen) throw new Error("диалог закрылся без сохранения");
    if (!err.trim()) throw new Error("нет ошибки");
    return err.trim().slice(0, 50);
  });

  await stepDap("U2 создание сервера stdio", async () => {
    await page.fill("#sf-name", "dap");
    await page.selectOption("#sf-transport", "stdio");
    await page.fill("#sf-command", DAP);
    await page.click("#srv-save");
    await page.waitForTimeout(800);
    const s = await state();
    const srv = (s.servers || []).find(x => x.name === "dap");
    if (!srv) throw new Error("сервера нет");
    if (srv.command[0] !== DAP) throw new Error(JSON.stringify(srv.command));
    if (!(await page.isVisible("#all-srv-table"))) throw new Error("таблица не показана");
  });

  await stepDap("U3 правка сервера сохраняет env, cwd и описание", async () => {
    await go("#/servers");
    await page.click('#all-srv-table tr[data-name="dap"] [data-edit]');
    await page.waitForTimeout(400);
    await page.fill("#sf-desc", "debug adapter");
    await page.fill("#sf-env", "FOO=bar");
    await page.fill("#sf-cwd", "/tmp");
    await page.click("#srv-save");
    await page.waitForTimeout(800);
    const srv = ((await state()).servers || []).find(x => x.name === "dap");
    if ((srv.env || {}).FOO !== "bar") throw new Error("env " + JSON.stringify(srv.env));
    if (srv.cwd !== "/tmp") throw new Error("cwd " + srv.cwd);
    if (srv.description !== "debug adapter" && srv.desc !== "debug adapter") throw new Error("описание " + JSON.stringify(srv));
  });

  await stepDap("U4 проверка сервера кнопкой check", async () => {
    await page.click('#all-srv-table tr[data-name="dap"] [data-check]');
    const cell = await waitFor(async () => {
      const t = await page.textContent('#all-srv-table tbody tr[data-name="dap"]');
      return /answers|healthy|not running|unreachable|error/i.test(t) ? t : null;
    }, 20000);
    if (!cell) throw new Error("состояние не обновилось");
    return cell.replace(/\s+/g, " ").slice(0, 60);
  });

  await stepDap("U5 лог сервера из реестра", async () => {
    const visible = await page.isVisible('#all-srv-table tr[data-name="dap"] [data-log]');
    if (!visible) return "кнопка спрятана: сервер не привязан к набору";
    await page.click('#all-srv-table tr[data-name="dap"] [data-log]');
    await page.waitForTimeout(700);
    const open = await page.isVisible("#srvlog-dlg");
    const txt = (await page.textContent("#srvlog").catch(() => "")) || "";
    if (!open) throw new Error("диалог не открылся");
    await page.click("#srvlog-close");
    await page.waitForTimeout(300);
    if (await page.isVisible("#srvlog-dlg")) throw new Error("диалог не закрылся");
    return txt.trim().split("\n").length + " строк";
  });

  await stepDap("V1 привязка сервера к набору", async () => {
    await go("#/set/alpha/servers");
    await page.selectOption("#srv-attach", "dap");
    await page.click("#srv-attach-add");
    const bound = await waitFor(async () => ((await setOf("alpha")).servers || []).indexOf("dap") >= 0, 8000);
    if (!bound) throw new Error("не привязан");
    const row = await waitFor(async () => (await page.locator('#set-srv-body tr[data-name="dap"]').count()) > 0, 8000);
    if (!row) throw new Error("нет строки в таблице набора");
  });

  await stepDap("V2 кнопка tools у сервера набора открывает вкладку с фильтром", async () => {
    await page.click('#set-srv-body tr[data-name="dap"] [data-tools]');
    await page.waitForTimeout(600);
    const h = await page.evaluate(() => location.hash);
    const sel = await page.inputValue("#tools-server");
    if (h !== "#/set/alpha/tools") throw new Error("адрес " + h);
    if (sel !== "dap") throw new Error("фильтр " + sel);
  });

  await stepDap("V3 проверка сервера набора", async () => {
    await go("#/set/alpha/servers");
    await page.click('#set-srv-body tr[data-name="dap"] [data-check]');
    const t = await waitFor(async () => {
      const row = await page.textContent('#set-srv-body tr[data-name="dap"]');
      return /answers|healthy|not running|unreachable|error|ошибк/i.test(row) ? row : null;
    }, 20000);
    if (!t) throw new Error("состояние не обновилось");
    return t.replace(/\s+/g, " ").slice(0, 60);
  });

  await stepDap("W1 запуск набора кнопкой Start", async () => {
    await go("#/set/alpha");
    const label = await page.textContent("#run");
    await page.click("#run");
    const a = await waitFor(async () => { const s = await setOf("alpha"); return s && s.runtime && s.runtime.running ? s : null; }, 30000);
    if (!a) throw new Error("не запустился");
    await page.waitForTimeout(600);
    const after = await page.textContent("#run");
    if (label.trim() === after.trim()) throw new Error("подпись кнопки не сменилась: " + after);
    return `«${label.trim()}» → «${after.trim()}»`;
  });

  await stepDap("W2 перезапуск набора", async () => {
    await page.click("#restart");
    // перезапуск = стоп + старт: ждём состояния, а не времени
    const a = await waitFor(async () => { const s = await setOf("alpha"); return s && s.runtime && s.runtime.running ? s : null; }, 30000);
    if (!a) throw new Error("после перезапуска не работает");
  });

  await stepDap("W3 инструменты: таблица, фильтр и одобрение", async () => {
    await go("#/set/alpha/tools");
    const rows = await waitFor(async () => { const n = await page.locator("#lock-body tr").count(); return n > 0 ? n : null; }, 40000);
    if (!rows) {
      const diag = await page.evaluate(async () => {
        const t = await (await fetch("/api/sets/alpha/tools")).json().catch(e => ({ err: String(e) }));
        const s = await (await fetch("/api/state")).json().catch(() => ({}));
        const set = (s.sets || []).find(x => x.id === "alpha") || {};
        return { tools: JSON.stringify(t).slice(0, 200), running: !!(set.runtime && set.runtime.running), hash: location.hash, rows: document.querySelectorAll("#lock-body tr").length, empty: (document.getElementById("lock-body") || {}).innerHTML?.length };
      });
      throw new Error("таблица пуста: " + JSON.stringify(diag).slice(0, 300) + " tools=" + JSON.stringify(toolsLog.slice(-6)));
    }
    const opts = await page.locator("#tools-server option").count();
    if (opts < 2) throw new Error("нет фильтра по серверам");
    const btn = page.locator("#lock-body [data-approve]").first();
    const shown = await btn.isVisible().catch(() => false);
    if (shown) {
      await btn.click();
      await page.waitForTimeout(1200);
      const changed = await page.locator("#lock-body tr .badge-warn").count();
      return `${rows} строк, одобрено, changed ${changed}`;
    }
    return `${rows} строк, одобрять нечего`;
  });

  await stepDap("W4 колонка tools берёт число из каталога набора без проверки", async () => {
    // чистая страница: проба и каталог в памяти панели пустеют только с
    // перезагрузкой документа, и число обязано появиться само, из каталога
    await page.evaluate(() => { location.hash = "#/set/alpha/servers"; });
    await page.reload();
    const cell = await waitFor(async () => {
      const t = (await page.textContent('#set-srv-body tr[data-name="dap"] td.num')).trim();
      return t && t !== "—" ? t : null;
    }, 20000);
    if (!cell) throw new Error("колонка tools пуста до проверки");
    // реестр хоста живёт тем же числом: сервер из работающего набора известен
    await page.evaluate(() => { location.hash = "#/host"; });
    await page.reload();
    const reg = await waitFor(async () => {
      const t = (await page.textContent('#servers tr[data-name="dap"] td.num')).trim();
      return t && t !== "—" ? t : null;
    }, 20000);
    if (!reg) throw new Error("в реестре хоста tools пуст до проверки");
    return `набор ${cell}, реестр ${reg}`;
  });

  await step("X1 выдача ключа с подписью", async () => {
    await go("#/set/alpha/keys");
    // Ждём по имени, а не по количеству: выдача ключа перезапускает набор,
    // список может перерисоваться и потерять или переставить прежние строки,
    // а фиксированная пауза под нагрузкой не дожидается ответа демона.
    const beforeIds = (await page.locator("#key-list li .key-name").allTextContents()).map(s => s.trim());
    await page.fill("#key-label", "cli");
    await page.click("#key-form button[type=submit]");
    const fresh = await waitFor(async () => {
      const ids = (await page.locator("#key-list li .key-name").allTextContents()).map(s => s.trim());
      return ids.find(id => id && beforeIds.indexOf(id) < 0) || null;
    }, 60000);
    const box = (await page.textContent("#new-key-box").catch(() => "")) || "";
    if (!fresh) {
      const now = (await page.locator("#key-list li .key-name").allTextContents()).map(s => s.trim());
      throw new Error(`нового ключа нет в списке за 60 с: было [${beforeIds.join(", ")}], сейчас [${now.join(", ")}]`);
    }
    if (box.indexOf("shown") < 0 && box.trim().length < 5) throw new Error("нет подсказки «показывается один раз»");
    const a = await setOf("alpha");
    if (!a.keys.some(k => k.id === fresh)) throw new Error(`ключа ${fresh} нет в состоянии`);
    if (!a.keys.some(k => k.label === "cli")) throw new Error("подписи нет в состоянии");
    return box.trim().slice(0, 40);
  });

  await step("X2 подпись ключа можно изменить и сохранить", async () => {
    const input = page.locator("#key-list li .key-note .input").first();
    await input.fill("cli-2");
    await page.locator("#key-list [data-note-save]").first().click();
    const saved = await waitFor(async () => (await setOf("alpha")).keys.some(k => k.label === "cli-2"), 15000);
    const a = await setOf("alpha");
    if (!saved) throw new Error(JSON.stringify(a.keys.map(k => k.label)));
  });

  await step("X3 отзыв ключа помечает строку и (на работающем наборе) предупреждает", async () => {
    await go("#/set/alpha/keys");
    const active = page.locator("#key-list li[data-revoked='false'] [data-revoke]");
    if (!(await active.count())) throw new Error("нет активного ключа для отзыва");
    await active.first().click();
    // отзыв активного ключа останавливает набор: под нагрузкой остановка идёт
    // десятками секунд, поэтому ждём до 30 с (панель всё это время говорит
    // «отзываем ключ и останавливаем набор»)
    const revoked = await waitFor(async () => (await page.locator('#key-list li[data-revoked="true"]').count()) > 0, 30000);
    if (!revoked) {
      const d = await page.evaluate(async () => {
        const k = await (await fetch("/api/sets/alpha/keys")).json().catch(e => ({ err: String(e) }));
        const s = await (await fetch("/api/state")).json().catch(() => ({}));
        const set = (s.sets || []).find(x => x.id === "alpha") || {};
        return { keys: JSON.stringify(k).slice(0, 240), running: !!(set.runtime && set.runtime.running),
                 items: [...document.querySelectorAll("#key-list li")].map(li => li.dataset.revoked + ":" + (li.dataset.key || "")).join(","),
                 warn: (document.getElementById("keys-warn") || {}).textContent, warnHidden: (document.getElementById("keys-warn") || {}).hidden };
      });
      throw new Error("нет строки с data-revoked=true: " + JSON.stringify(d).slice(0, 420));
    }
    const warn = await page.isVisible("#keys-warn");
    return "отозвано, предупреждение " + (warn ? "показано" : "нет");
  });

  await step("Y1 удаление набора: отмена подтверждения ничего не удаляет", async () => {
    await go("#/set/beta");
    dialogMode = "dismiss";
    await page.click("#set-del");
    await page.waitForTimeout(700);
    dialogMode = "accept";
    if (!(await setOf("beta"))) throw new Error("набор удалён при отмене");
  });

  await step("Y2 удаление набора: подтверждение удаляет и уводит на список", async () => {
    await go("#/set/beta");
    await page.click("#set-del");
    await page.waitForTimeout(1200);
    if (await setOf("beta")) throw new Error("beta остался");
    const a = await setOf("alpha");
    if (!a) throw new Error("alpha пропал");
    const h = await page.evaluate(() => location.hash);
    const list = await page.isVisible("#sets-list");
    if (h !== "#/sets" || !list) throw new Error("после удаления адрес " + h + ", список " + list);
  });

  await step("Y3 пустые состояния: вкладки пустого набора", async () => {
    await page.click("#add-toggle");
    await page.fill("#add-name", "empty");
    // порт не задаём: жёсткий 9191 держали остатки прошлых прогонов, и набор не
    // создавался — панель сама берёт первый свободный
    await page.click("#addform button[type=submit]");
    await waitFor(async () => (await setOf("empty")), 8000);
    await go("#/set/empty/servers");
    const srvEmpty = await waitFor(async () => page.isVisible("#set-srv-empty"), 20000, 300);
    await go("#/set/empty/tools");
    const toolsEmpty = await waitFor(async () => page.isVisible("#tools-empty"), 20000, 300);
    await go("#/set/empty/calls");
    await page.waitForTimeout(800);
    const callsEmpty = await page.evaluate(() => document.querySelectorAll("#calls-body tr").length === 0);
    if (!srvEmpty) throw new Error("нет пустого состояния серверов");
    if (!toolsEmpty) throw new Error("нет пустого состояния инструментов");
    if (!callsEmpty) throw new Error("вызовы появились у пустого набора");
  });

  await step("Z1 375 px: страницы не скроллятся вбок", async () => {
    await page.setViewportSize({ width: 375, height: 812 });
    const routes = ["#/", "#/sets", "#/servers", "#/vars", "#/stats/today", "#/set/alpha",
      "#/set/alpha/servers", "#/set/alpha/keys", "#/set/alpha/tools", "#/set/alpha/calls", "#/set/alpha/logs"];
    const wide = [];
    for (const r of routes) {
      await go(r);
      const m = await page.evaluate(() => [document.documentElement.scrollWidth, document.documentElement.clientWidth]);
      if (m[0] > m[1]) wide.push(r + " " + m.join("/"));
    }
    if (wide.length) throw new Error(wide.join(", "));
    return routes.length + " маршрутов";
  });

  await step("Z2 тач-цели не меньше 32 px на 375", async () => {
    const small = [];
    for (const r of ["#/", "#/set/alpha/servers", "#/set/alpha/keys"]) {
      await go(r);
      const s = await page.evaluate(() => Array.from(document.querySelectorAll("button, a"))
        .filter(e => e.offsetParent !== null && !e.closest(".rail"))
        .map(e => { const b = e.getBoundingClientRect(); return { t: (e.id || e.textContent || e.tagName).trim().slice(0, 24), w: Math.round(b.width), h: Math.round(b.height) }; })
        .filter(x => x.h > 0 && x.h < 30));
      if (s.length) small.push(r + ": " + s.slice(0, 4).map(x => `${x.t} ${x.w}x${x.h}`).join(", "));
    }
    if (small.length) throw new Error(small.join(" | "));
  });

  await step("Z3 консоль без ошибок, HTTP без неожиданных отказов", async () => {
    const unexpected = httpErrors.filter(x => !/^(409|400|404) (POST|GET) /.test(x));
    if (consoleErrors.length) throw new Error("console: " + consoleErrors.slice(0, 3).join(" | "));
    if (unexpected.length) throw new Error("http: " + unexpected.slice(0, 5).join(" | "));
    return "ожидаемых отказов " + httpErrors.length + ", диалогов " + dialogs.length;
  });

  await browser.close();
  const failed = results.filter(r => !r.pass);
  if (process.env.ACC_OUT) {
    require("fs").writeFileSync(process.env.ACC_OUT, JSON.stringify(results, null, 1), { mode: 0o600 });
  }
  console.log(JSON.stringify({ total: results.length, skipped: skipped.length, failed: failed.length, failedList: failed.map(f => f.name + " :: " + f.why) }, null, 1));
  process.exit(failed.length ? 1 : 0);
})();
