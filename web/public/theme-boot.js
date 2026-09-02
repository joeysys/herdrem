(function () {
  try {
    var u = new URLSearchParams(window.location.search).get('theme')
    var t = u || localStorage.getItem('herdrem_theme') || 'dark'
    // Resolve meta-themes that follow system prefers-color-scheme
    if (t === 'default') t = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
    else if (t === 'modern') t = matchMedia('(prefers-color-scheme: dark)').matches ? 'modern-dark' : 'modern-light'
    document.documentElement.dataset.theme = t
  } catch (e) {}
})()
