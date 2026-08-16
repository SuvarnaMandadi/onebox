import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { ThemeProvider } from '@/components/theme-provider'
import { AuthProvider } from '@/lib/auth'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <TooltipProvider>
        <AuthProvider>
          <BrowserRouter basename="/app">
            <App />
          </BrowserRouter>
          <Toaster position="bottom-right" />
        </AuthProvider>
      </TooltipProvider>
    </ThemeProvider>
  </StrictMode>,
)
