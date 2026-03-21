import { Routes, Route, Navigate } from 'react-router-dom'
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
import ExportsPage from './pages/ExportsPage'

function App() {
  // TODO: Check auth state
  const isAuthenticated = true

  if (!isAuthenticated) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
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
        <Route path="/exports" element={<ExportsPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="/config" element={<ConfigPage />} />
      </Route>
    </Routes>
  )
}

export default App
