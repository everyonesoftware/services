import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { AuthGate } from './AuthGate.tsx'
import { registerPwa } from './registerPwa.ts'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AuthGate />
  </StrictMode>,
)

if (import.meta.env.PROD && window.isSecureContext && 'serviceWorker' in navigator) {
  void registerPwa(navigator.serviceWorker)
}
