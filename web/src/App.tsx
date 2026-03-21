import { Routes, Route, Navigate } from 'react-router-dom'
import { useEffect } from 'react'
import { useAuthStore } from './stores/authStore'
import { useWebSocketBridge } from './hooks/useWebSocketBridge'
import wsClient from './api/websocket'
import Layout from './components/Layout'
import LoginPage from './pages/LoginPage'
import MapPage from './pages/MapPage'
import NodesPage from './pages/NodesPage'
import DronesPage from './pages/DronesPage'
import ConfigPage from './pages/ConfigPage'
import AlertsPage from './pages/AlertsPage'
import ChatPage from './pages/ChatPage'
import UsersPage from './pages/UsersPage'
import GeofencesPage from './pages/GeofencesPage'
import TargetsPage from './pages/TargetsPage'
import InventoryPage from './pages/InventoryPage'
import CommandsPage from './pages/CommandsPage'
import WebhooksPage from './pages/WebhooksPage'
import ADSBPage from './pages/ADSBPage'
import ACARSPage from './pages/ACARSPage'
import TerminalPage from './pages/TerminalPage'
import ExportsPage from './pages/ExportsPage'

function AuthenticatedApp() {
  useWebSocketBridge()

  return (
    <Routes>
      <Route path="/login" element={<Navigate to="/" replace />} />
      <Route element={<Layout />}>
        <Route path="/" element={<Navigate to="/map" replace />} />
        <Route path="/map" element={<MapPage />} />
        <Route path="/nodes" element={<NodesPage />} />
        <Route path="/drones" element={<DronesPage />} />
        <Route path="/alerts" element={<AlertsPage />} />
        <Route path="/chat" element={<ChatPage />} />
        <Route path="/targets" element={<TargetsPage />} />
        <Route path="/geofences" element={<GeofencesPage />} />
        <Route path="/inventory" element={<InventoryPage />} />
        <Route path="/commands" element={<CommandsPage />} />
        <Route path="/webhooks" element={<WebhooksPage />} />
        <Route path="/adsb" element={<ADSBPage />} />
        <Route path="/acars" element={<ACARSPage />} />
        <Route path="/terminal" element={<TerminalPage />} />
        <Route path="/exports" element={<ExportsPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="/config" element={<ConfigPage />} />
      </Route>
    </Routes>
  )
}

function App() {
  const { isAuthenticated, token, stage, initialize } = useAuthStore()

  useEffect(() => {
    initialize()
  }, [initialize])

  useEffect(() => {
    if (token && isAuthenticated) {
      wsClient.connect()
    }
    return () => wsClient.disconnect()
  }, [token, isAuthenticated])

  if (stage === 'idle' || stage === 'checking') {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <div className="inline-block w-8 h-8 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  if (!isAuthenticated) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }

  return <AuthenticatedApp />
}

export default App
