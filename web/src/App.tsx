import { useEffect } from 'react'
import { BrowserRouter, useNavigate } from 'react-router-dom'
import { AuthProvider } from './auth/AuthProvider'
import { onUnauthorized } from './lib/api'
import { AppRoutes } from './app/router'

function UnauthorizedRedirect() {
  const navigate = useNavigate()
  useEffect(() => onUnauthorized(() => navigate('/auth')), [navigate])
  return null
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <UnauthorizedRedirect />
        <AppRoutes />
      </AuthProvider>
    </BrowserRouter>
  )
}
