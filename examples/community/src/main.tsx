import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './lib/i18n'
import { ThemeProvider } from './lib/theme'
import Community from './Community'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <Community />
    </ThemeProvider>
  </StrictMode>,
)
