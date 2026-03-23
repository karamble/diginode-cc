import { useEffect, useRef } from 'react'
import { useNotificationStore, type AppNotification } from '../stores/notificationStore'

const SEVERITY_COLORS: Record<string, string> = {
  info: 'border-l-blue-500 bg-blue-600/5',
  notice: 'border-l-green-500 bg-green-600/5',
  alert: 'border-l-orange-500 bg-orange-600/5',
  critical: 'border-l-red-500 bg-red-600/5',
}

const SEVERITY_BADGE: Record<string, string> = {
  info: 'bg-blue-600/20 text-blue-400',
  notice: 'bg-green-600/20 text-green-400',
  alert: 'bg-orange-600/20 text-orange-400',
  critical: 'bg-red-600/20 text-red-400',
}

const TYPE_LABEL: Record<string, string> = {
  geofence: 'GEO',
  chat: 'MSG',
  alert: 'ALT',
  drone: 'UAV',
  system: 'SYS',
}

function timeAgo(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime()
  const secs = Math.floor(diff / 1000)
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

function NotificationRow({ n, onDismiss }: { n: AppNotification; onDismiss: (id: string) => void }) {
  const colors = SEVERITY_COLORS[n.severity] || SEVERITY_COLORS.info
  const badge = SEVERITY_BADGE[n.severity] || SEVERITY_BADGE.info
  const typeLabel = TYPE_LABEL[n.type] || n.type.toUpperCase().slice(0, 3)

  return (
    <div className={`border-l-2 ${colors} px-3 py-2 flex items-start gap-2 ${n.read ? 'opacity-60' : ''}`}>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-1.5 mb-0.5">
          <span className={`px-1 py-px text-[9px] font-bold rounded ${badge}`}>{typeLabel}</span>
          <span className="text-xs font-medium text-dark-200 truncate">{n.title}</span>
        </div>
        {n.message && (
          <p className="text-[11px] text-dark-400 truncate">{n.message}</p>
        )}
        <span className="text-[10px] text-dark-600">{timeAgo(n.timestamp)}</span>
      </div>
      <button
        onClick={(e) => { e.stopPropagation(); onDismiss(n.id) }}
        className="flex-shrink-0 p-0.5 text-dark-600 hover:text-dark-300 transition-colors"
        title="Dismiss"
      >
        <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
  )
}

export default function NotificationPanel({ onClose }: { onClose: () => void }) {
  const { notifications, unreadCount, markAllRead, dismiss, clearAll } = useNotificationStore()
  const panelRef = useRef<HTMLDivElement>(null)

  // Close on click outside
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        onClose()
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [onClose])

  return (
    <div
      ref={panelRef}
      className="absolute right-0 top-full mt-1 w-80 max-h-96 bg-surface border border-dark-700/50 rounded-lg shadow-xl overflow-hidden z-50"
    >
      {/* Header */}
      <div className="px-3 py-2 border-b border-dark-700/50 flex items-center justify-between">
        <span className="text-xs font-semibold text-dark-200">
          Notifications {unreadCount > 0 && <span className="text-dark-500">({unreadCount} new)</span>}
        </span>
        <div className="flex items-center gap-2">
          {unreadCount > 0 && (
            <button
              onClick={markAllRead}
              className="text-[10px] text-primary-400 hover:text-primary-300 transition-colors"
            >
              Mark all read
            </button>
          )}
          {notifications.length > 0 && (
            <button
              onClick={clearAll}
              className="text-[10px] text-dark-500 hover:text-dark-300 transition-colors"
            >
              Clear
            </button>
          )}
        </div>
      </div>

      {/* Notification list */}
      <div className="overflow-y-auto max-h-80 divide-y divide-dark-800/50">
        {notifications.length === 0 ? (
          <div className="px-3 py-6 text-center text-dark-600 text-xs">
            No notifications
          </div>
        ) : (
          notifications.map((n) => (
            <NotificationRow key={n.id} n={n} onDismiss={dismiss} />
          ))
        )}
      </div>
    </div>
  )
}
