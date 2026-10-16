// Apply the saved theme and text size (boot.js) before styles first apply.
// A theme requested by the page's address takes precedence. Waiting for
// app.js at the foot of the page let the header paint in the system theme
// and default size first, then change as the page loaded.
(function () {
  var asked = new URLSearchParams(location.search).get("theme");
  var prefs = window.bootPrefs;
  var saved = prefs && prefs.theme;
  var theme = asked || (saved && saved !== "system" ? saved : "");
  if (theme) document.documentElement.dataset.theme = theme;
  if (prefs && prefs.colorblind) document.documentElement.dataset.colorblind = "1";
  if (prefs) document.documentElement.style.setProperty("--zoom", prefs.web ? 1 : (prefs.textSize || 100) / 100);
  // The content's width on a large screen (settings.PageWidth): 0 is the
  // default 1200, -1 the whole window (a half no window is wider than).
  if (prefs && prefs.pageWidth) document.documentElement.style.setProperty("--page-half", prefs.pageWidth < 0 ? "100000px" : prefs.pageWidth / 2 + "px");
})();
