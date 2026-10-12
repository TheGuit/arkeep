import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { registerSW } from 'virtual:pwa-register'
// Fonts are bundled rather than loaded from Google Fonts: a self-hosted
// instance must not make every browser that opens it call a third party.
import '@fontsource/be-vietnam-pro/300.css'
import '@fontsource/be-vietnam-pro/400.css'
import '@fontsource/be-vietnam-pro/400-italic.css'
import '@fontsource/be-vietnam-pro/500.css'
import '@fontsource/be-vietnam-pro/600.css'
import '@fontsource/be-vietnam-pro/700.css'
import './style.css'
import App from './App.vue'
import { router } from './router'

// Register the service worker and handle automatic updates.
//
// When a new Docker image is deployed, new JS chunks with new content hashes
// are served. The browser detects the changed sw.js, installs the new SW in
// the background, and — because workbox uses skipWaiting + clientsClaim — the
// new SW activates immediately and takes control of the page.
//
// Without this call, nothing handles the resulting `controllerchange` event,
// and the page keeps running with old HTML that references old chunk hashes
// no longer in the new SW's precache → dynamic import errors on navigation.
//
// registerSW() wires up:
//   navigator.serviceWorker.addEventListener('controllerchange', () => reload())
// so the page reloads cleanly the moment the new SW takes over.
registerSW({ immediate: true })

const pinia = createPinia()
const app = createApp(App)

app.use(router)
app.use(pinia)
app.mount('#app')
