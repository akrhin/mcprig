/* Тема и язык ставятся до первой отрисовки: скрипт подключён без defer, выше app.js. */
(function () {
  'use strict';
  var KEY = 'mcprig.theme';
  var LANG_KEY = 'mcprig.lang';
  var ALIAS = { mcprig: 'dark', tokyo: 'dark', solarized: 'light' };
  var KNOWN = { dark: 1, light: 1, nord: 1, gruvbox: 1 };
  var html = document.documentElement;
  var theme = 'dark';
  try {
    var saved = localStorage.getItem(KEY) || '';
    theme = KNOWN[saved] ? saved : (ALIAS[saved] || 'dark');
    if (saved && saved !== theme) { localStorage.setItem(KEY, theme); }
  } catch (e) { /* приватный режим */ }
  html.setAttribute('data-theme', theme);

  var lang = 'en';
  try { lang = localStorage.getItem(LANG_KEY) === 'ru' ? 'ru' : 'en'; } catch (e) { /* приватный режим */ }
  html.setAttribute('lang', lang);
})();
