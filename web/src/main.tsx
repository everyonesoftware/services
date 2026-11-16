import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { AuthGate } from './AuthGate.tsx'
import { retirePwa } from './retirePwa.ts'
import './Mobile.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AuthGate />
  </StrictMode>,
)

if (window.isSecureContext) {
  void retirePwa(navigator.serviceWorker, window.caches, window.location.origin)
}
