import { vi } from 'vitest'

// jsdom has no matchMedia; pages read it for the theme and reduced motion.
// Every query answers "no match", as in a browser without the preference.
vi.stubGlobal('matchMedia', (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => {},
  removeEventListener: () => {},
  addListener: () => {},
  removeListener: () => {},
  dispatchEvent: () => false,
}))
