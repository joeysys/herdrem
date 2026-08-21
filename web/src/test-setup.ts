// jsdom lacks matchMedia / scrollIntoView — provide stubs.
// Tests can call (window as any).__setStandalone(v) to simulate PWA mode,
// which toggles the '(display-mode: standalone)' media query.

let standalone = false
;(window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone = (v: boolean) => {
  standalone = v
}

window.matchMedia = ((query: string) => ({
  matches: query === '(display-mode: standalone)' ? standalone : false,
  media: query,
  onchange: null,
  addEventListener: () => {},
  removeEventListener: () => {},
  addListener: () => {},
  removeListener: () => {},
  dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia

if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}

// Force a mobile viewport so useLayoutMode detects mobile layout.
// (useLayoutMode reads window.innerWidth at module load, and setupFiles run
// before test-file imports, so this takes effect for the whole suite.)
Object.defineProperty(window, 'innerWidth', { writable: true, configurable: true, value: 390 })
Object.defineProperty(window, 'innerHeight', { writable: true, configurable: true, value: 800 })
