import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useAuthStore } from '../stores/authStore'
import wsClient from '../api/websocket'

const PAGE_TITLES: Record<string, string> = {
  '/map': 'Map',
  '/nodes': 'Nodes',
  '/drones': 'Drones',
  '/alerts': 'Alerts',
  '/chat': 'Chat',
  '/targets': 'Targets',
  '/geofences': 'Geofences',
  '/inventory': 'Devices',
  '/commands': 'Commands',
  '/adsb': 'ADS-B',
  '/acars': 'ACARS',
  '/webhooks': 'Webhooks',
  '/terminal': 'Terminal',
  '/exports': 'Exports',
  '/users': 'Users',
  '/config': 'Config',
}

const ROLE_BADGE: Record<string, string> = {
  ADMIN: 'bg-red-600/20 text-red-400 border-red-500/30',
  OPERATOR: 'bg-orange-600/20 text-orange-400 border-orange-500/30',
  ANALYST: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
  VIEWER: 'bg-dark-600/20 text-dark-300 border-dark-500/30',
}

export default function Header() {
  const location = useLocation()
  const { user, logout } = useAuthStore()
  const [wsConnected, setWsConnected] = useState(false)

  useEffect(() => {
    const onConnect = () => setWsConnected(true)
    const onDisconnect = () => setWsConnected(false)

    // Check initial state by listening for any init event
    const onInit = () => setWsConnected(true)
    wsClient.on('init', onInit)

    // Poll connection state via a simple heuristic:
    // we mark connected when we get any event, disconnected on a timer
    const allEvents = [
      'init', 'drone.telemetry', 'drone.status', 'drone.remove',
      'node.update', 'node.remove', 'node.position',
      'alert', 'chat.message', 'health',
    ]
    allEvents.forEach((evt) => wsClient.on(evt, onConnect))

    // Heartbeat check: if no events for 35s, mark disconnected
    let timeout: ReturnType<typeof setTimeout>
    const resetTimeout = () => {
      clearTimeout(timeout)
      setWsConnected(true)
      timeout = setTimeout(onDisconnect, 35000)
    }
    allEvents.forEach((evt) => wsClient.on(evt, resetTimeout))

    return () => {
      clearTimeout(timeout)
      wsClient.off('init', onInit)
      allEvents.forEach((evt) => {
        wsClient.off(evt, onConnect)
        wsClient.off(evt, resetTimeout)
      })
    }
  }, [])

  const handleLogout = () => {
    localStorage.removeItem('cc_token')
    logout()
  }

  const pageTitle = PAGE_TITLES[location.pathname] || 'DigiNode CC'
  const roleBadge = user?.role ? ROLE_BADGE[user.role] || ROLE_BADGE.VIEWER : ''

  return (
    <header className="h-12 px-4 border-b border-dark-700/50 bg-surface flex items-center justify-between flex-shrink-0">
      {/* Left: page title */}
      <div className="flex items-center gap-3">
        <h1 className="text-sm font-semibold text-dark-100">{pageTitle}</h1>
      </div>

      {/* Right: connection status, user info, logout */}
      <div className="flex items-center gap-4">
        {/* Connection status */}
        <div className="flex items-center gap-1.5" title={wsConnected ? 'WebSocket connected' : 'WebSocket disconnected'}>
          <span
            className={`inline-block w-2 h-2 rounded-full ${
              wsConnected
                ? 'bg-status-friendly animate-pulse'
                : 'bg-dark-600'
            }`}
          />
          <span className="text-[10px] text-dark-500">
            {wsConnected ? 'Live' : 'Offline'}
          </span>
        </div>

        {/* User info */}
        {user && (
          <div className="flex items-center gap-2">
            <span className="text-xs text-dark-300">{user.email}</span>
            {user.role && (
              <span className={`px-1.5 py-0.5 text-[10px] font-medium rounded border ${roleBadge}`}>
                {user.role}
              </span>
            )}
          </div>
        )}

        {/* Logout */}
        <button
          onClick={handleLogout}
          className="px-2.5 py-1 text-xs text-dark-400 hover:text-dark-200 hover:bg-dark-800/50 rounded transition-colors"
          title="Sign out"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
          </svg>
        </button>
      </div>
    </header>
  )
}
