import { useEffect } from 'react'
import { BrowserRouter, useNavigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { AuthProvider } from './auth/AuthProvider'
import { onUnauthorized } from './lib/api'
import { queryClient } from './lib/queryClient'
import { AppRoutes } from './app/router'

function UnauthorizedRedirect() {
  const navigate = useNavigate()
  useEffect(() => onUnauthorized(() => navigate('/auth')), [navigate])
  return null
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <UnauthorizedRedirect />
          <AppRoutes />
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
