import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useAuthStore } from '../stores/authStore'
import { useNotificationStore } from '../stores/notificationStore'
import NotificationPanel from './NotificationPanel'
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
  const [serialConnected, setSerialConnected] = useState(false)
  const [heartbeat, setHeartbeat] = useState(false)
  const [showNotifications, setShowNotifications] = useState(false)
  const localNodeNum = useRef<number | null>(null)
  const unreadCount = useNotificationStore((s) => s.unreadCount)

  useEffect(() => {
    // Heartbeat: brief blink on meaningful mesh data, then back to idle
    let timeout: ReturnType<typeof setTimeout>
    const triggerHeartbeat = () => {
      clearTimeout(timeout)
      setHeartbeat(true)
      timeout = setTimeout(() => setHeartbeat(false), 2000)
    }

    // Track serial (Heltec) connection state from init + health events
    const onInit = (payload: any) => {
      if (payload?.serial?.connected !== undefined) {
        setSerialConnected(payload.serial.connected)
      }
      // Cache the local gateway node number for heartbeat filtering
      if (Array.isArray(payload?.nodes)) {
        const local = payload.nodes.find((n: any) => n.isLocal)
        if (local?.nodeNum) {
          localNodeNum.current = local.nodeNum
        }
      }
    }
    const onHealth = (payload: any) => {
      if (payload?.serial?.connected !== undefined) {
        setSerialConnected(payload.serial.connected)
      }
    }
    wsClient.on('init', onInit)
    wsClient.on('health', onHealth)

    // Node events: only from remote nodes (skip local gateway)
    const onNodeEvent = (payload: any) => {
      if (payload?.isLocal) return
      if (localNodeNum.current && payload?.nodeNum === localNodeNum.current) return
      triggerHeartbeat()
    }
    wsClient.on('node.update', onNodeEvent)
    wsClient.on('node.remove', onNodeEvent)
    wsClient.on('node.position', onNodeEvent)

    // Chat: only from remote nodes
    const onChat = (payload: any) => {
      if (localNodeNum.current && payload?.fromNode === localNodeNum.current) return
      triggerHeartbeat()
    }
    wsClient.on('chat.message', onChat)

    // Drones, alerts, inventory, targets: always blink (external detections)
    const onAlways = () => triggerHeartbeat()
    wsClient.on('drone.telemetry', onAlways)
    wsClient.on('drone.status', onAlways)
    wsClient.on('drone.remove', onAlways)
    wsClient.on('alert', onAlways)
    wsClient.on('inventory.update', onAlways)
    wsClient.on('target.update', onAlways)

    return () => {
      clearTimeout(timeout)
      wsClient.off('init', onInit)
      wsClient.off('health', onHealth)
      wsClient.off('node.update', onNodeEvent)
      wsClient.off('node.remove', onNodeEvent)
      wsClient.off('node.position', onNodeEvent)
      wsClient.off('chat.message', onChat)
      wsClient.off('drone.telemetry', onAlways)
      wsClient.off('drone.status', onAlways)
      wsClient.off('drone.remove', onAlways)
      wsClient.off('alert', onAlways)
      wsClient.off('inventory.update', onAlways)
      wsClient.off('target.update', onAlways)
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
        {/* Status indicators */}
        <div className="flex items-center gap-3">
          {/* Primary: Heltec serial connection */}
          <div className="flex items-center gap-1.5" title={serialConnected ? 'Heltec LoRa connected' : 'Heltec LoRa disconnected'}>
            <span
              className={`inline-block w-2 h-2 rounded-full ${
                serialConnected
                  ? 'bg-status-friendly animate-pulse'
                  : 'bg-dark-600'
              }`}
            />
            <span className="text-[10px] text-dark-500">
              {serialConnected ? 'Mesh Online' : 'Mesh Offline'}
            </span>
          </div>

          {/* Secondary: remote mesh data activity (only when serial is connected) */}
          {serialConnected && (
            <div className="flex items-center gap-1" title={heartbeat ? 'Receiving data from remote mesh nodes' : 'Waiting for remote mesh data'}>
              <span
                className={`inline-block w-1.5 h-1.5 rounded-full ${
                  heartbeat
                    ? 'bg-blue-400 animate-pulse'
                    : 'bg-dark-700'
                }`}
              />
              <span className="text-[10px] text-dark-600">
                {heartbeat ? 'Data' : 'Idle'}
              </span>
            </div>
          )}
        </div>

        {/* Notification bell */}
        <div className="relative">
          <button
            onClick={() => setShowNotifications((v) => !v)}
            className="relative p-1.5 text-dark-400 hover:text-dark-200 hover:bg-dark-800/50 rounded transition-colors"
            title="Notifications"
          >
            <svg className="w-5 h-5" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
            </svg>
            {unreadCount > 0 && (
              <span className="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 px-1 flex items-center justify-center text-[10px] font-bold text-white bg-red-500 rounded-full">
                {unreadCount > 99 ? '99+' : unreadCount}
              </span>
            )}
          </button>
          {showNotifications && (
            <NotificationPanel onClose={() => setShowNotifications(false)} />
          )}
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
