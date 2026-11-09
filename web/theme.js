/*
  mcprig — панель. Тема до первой отрисовки: файл подключён в <head> без defer,
  поэтому скрипт выполняется раньше, чем браузер покажет body (в CSP inline нельзя).
  Значение ключа совпадает с <select id="theme"> в index.html.
*/
(function () {
  var KEY = 'mcprig.theme';
  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (e) { /* приватный режим */ }
  var theme = saved || (window.matchMedia && matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'mcprig');
  document.documentElement.setAttribute('data-theme', theme);
}());
