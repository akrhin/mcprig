/* Тема и язык ставятся до первой отрисовки: скрипт подключён без defer, выше app.js.
   Две оси: data-theme — внешний вид (default | win98), data-palette — схема цветов.
   Схемы у каждой оси свои и помнятся отдельно: вернулся к теме — вернулись её цвета. */
(function () {
  'use strict';
  var KEY = 'mcprig.theme';
  var PKEY = 'mcprig.palette';
  var LANG_KEY = 'mcprig.lang';
  // старые имена схем — это палитры темы default
  var ALIAS = { mcprig: 'dark', tokyo: 'dark', solarized: 'light' };
  var SCHEMES = {
    'default': { dark: 1, light: 1, nord: 1, gruvbox: 1 },
    win98: { 'w98': 1, 'w98-cool': 1, 'w98-warm': 1, 'w98-hc': 1 }
  };
  var FIRST = { 'default': 'dark', win98: 'w98' };
  var html = document.documentElement;

  function store(key, val) { try { localStorage.setItem(key, val); } catch (e) { /* приватный режим */ } }
  function recall(key) { try { return localStorage.getItem(key) || ''; } catch (e) { return ''; } }
  function schemeKey(t) { return t === 'win98' ? PKEY + '.win98' : PKEY; }
  function fits(t, p) { return !!(SCHEMES[t] && SCHEMES[t][p]); }

  var theme = 'default';
  var palette = '';
  var raw = recall(KEY);
  if (raw === 'win98') {
    theme = 'win98';
  } else if (ALIAS[raw] || SCHEMES['default'][raw]) {
    palette = ALIAS[raw] || raw;
  }
  var saved = palette || recall(schemeKey(theme));
  palette = fits(theme, saved) ? saved : FIRST[theme];

  store(KEY, theme);
  store(schemeKey(theme), palette);
  html.setAttribute('data-theme', theme);
  html.setAttribute('data-palette', palette);

  var lang = 'en';
  try { lang = localStorage.getItem(LANG_KEY) === 'ru' ? 'ru' : 'en'; } catch (e) { /* приватный режим */ }
  html.setAttribute('lang', lang);
})();
