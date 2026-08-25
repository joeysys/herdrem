(function () {
  try {
    var u = new URLSearchParams(window.location.search).get('theme')
    var t = u || localStorage.getItem('herdrem_theme') || 'dark'
    document.documentElement.dataset.theme = t
  } catch (e) {}
})()
