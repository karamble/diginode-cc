import { useState, useEffect, useRef, useCallback } from 'react'
import wsClient from '../api/websocket'

interface LogEntry {
  id: number
  timestamp: Date
  eventType: string
  payload: string
}

type FilterType = 'All' | 'Drones' | 'Nodes' | 'Alerts' | 'Commands' | 'Raw'

const FILTER_EVENT_MAP: Record<FilterType, string[]> = {
  All: [],
  Drones: ['drone', 'drone.telemetry', 'drone.status', 'drone.remove', 'drone.update'],
  Nodes: ['node', 'node.update', 'node.position', 'node.telemetry', 'mesh'],
  Alerts: ['alert', 'alarm', 'geofence', 'warning'],
  Commands: ['command', 'cmd', 'response'],
  Raw: ['serial', 'raw', 'ble'],
}

const EVENT_TYPE_COLORS: Record<string, string> = {
  drone: 'bg-red-600/20 text-red-400 border-red-500/30',
  node: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
  alert: 'bg-orange-600/20 text-orange-400 border-orange-500/30',
  alarm: 'bg-red-600/20 text-red-400 border-red-500/30',
  command: 'bg-purple-600/20 text-purple-400 border-purple-500/30',
  serial: 'bg-green-600/20 text-green-400 border-green-500/30',
  mesh: 'bg-cyan-600/20 text-cyan-400 border-cyan-500/30',
  geofence: 'bg-yellow-600/20 text-yellow-400 border-yellow-500/30',
}

function getEventBadgeClass(eventType: string): string {
  for (const [key, cls] of Object.entries(EVENT_TYPE_COLORS)) {
    if (eventType.toLowerCase().includes(key)) return cls
  }
  return 'bg-dark-600/20 text-dark-300 border-dark-500/30'
}

function matchesFilter(eventType: string, filter: FilterType): boolean {
  if (filter === 'All') return true
  const patterns = FILTER_EVENT_MAP[filter]
  const lower = eventType.toLowerCase()
  return patterns.some(p => lower.includes(p))
}

const MAX_ENTRIES = 500

export default function TerminalPage() {
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [filter, setFilter] = useState<FilterType>('All')
  const [autoScroll, setAutoScroll] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)
  const idRef = useRef(0)

  const addEntry = useCallback((eventType: string, payload: unknown) => {
    const entry: LogEntry = {
      id: ++idRef.current,
      timestamp: new Date(),
      eventType,
      payload: typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2),
    }
    setEntries(prev => {
      const next = [...prev, entry]
      if (next.length > MAX_ENTRIES) return next.slice(-MAX_ENTRIES)
      return next
    })
  }, [])

  useEffect(() => {
    // Subscribe to all known event types
    const eventTypes = [
      'drone', 'drone.telemetry', 'drone.status', 'drone.remove', 'drone.update',
      'node', 'node.update', 'node.position', 'node.telemetry',
      'alert', 'alarm', 'geofence', 'warning',
      'command', 'cmd', 'response',
      'serial', 'raw', 'ble',
      'mesh', 'chat', 'health', 'status', 'adsb',
      'acars', 'webhook', 'target', 'inventory',
    ]

    const handlers = eventTypes.map(type => {
      const handler = (payload: unknown) => addEntry(type, payload)
      wsClient.on(type, handler)
      return { type, handler }
    })

    // Also listen for a wildcard-like catch-all if WebSocket dispatches unknown types
    const catchAll = (payload: unknown) => addEntry('unknown', payload)
    wsClient.on('*', catchAll)

    return () => {
      handlers.forEach(({ type, handler }) => wsClient.off(type, handler))
      wsClient.off('*', catchAll)
    }
  }, [addEntry])

  // Auto-scroll
  useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [entries.length, autoScroll])

  const handleScroll = () => {
    if (!scrollRef.current) return
    const { scrollTop, scrollHeight, clientHeight } = scrollRef.current
    const atBottom = scrollHeight - scrollTop - clientHeight < 40
    setAutoScroll(atBottom)
  }

  const filtered = filter === 'All'
    ? entries
    : entries.filter(e => matchesFilter(e.eventType, filter))

  const formatTime = (d: Date) => {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3 } as Intl.DateTimeFormatOptions)
  }

  const truncatePayload = (payload: string, maxLen: number = 200) => {
    if (payload.length <= maxLen) return payload
    return payload.slice(0, maxLen) + '...'
  }

  const filters: FilterType[] = ['All', 'Drones', 'Nodes', 'Alerts', 'Commands', 'Raw']

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="px-4 py-2.5 border-b border-dark-700/50 bg-surface/80 backdrop-blur-sm flex items-center justify-between z-10">
        <h2 className="text-sm font-semibold text-green-400 font-mono">
          Terminal
        </h2>
        <div className="flex items-center gap-3">
          {/* Filter buttons */}
          <div className="flex items-center gap-1">
            {filters.map(f => (
              <button
                key={f}
                onClick={() => setFilter(f)}
                className={`px-2.5 py-1 text-xs rounded font-medium transition-colors ${
                  filter === f
                    ? 'bg-green-600/20 text-green-400 border border-green-500/30'
                    : 'text-dark-400 hover:text-dark-200 hover:bg-dark-800/50'
                }`}
              >
                {f}
              </button>
            ))}
          </div>

          <span className="text-xs text-dark-500 font-mono">
            {filtered.length}/{entries.length}
          </span>

          <button
            onClick={() => setEntries([])}
            className="px-3 py-1 text-xs text-dark-400 hover:text-dark-200 bg-dark-800 hover:bg-dark-700 rounded font-medium transition-colors"
          >
            Clear
          </button>
        </div>
      </div>

      {/* Terminal body */}
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto bg-black font-mono text-xs p-3"
        style={{ minHeight: 0 }}
      >
        {filtered.length === 0 ? (
          <div className="text-green-700 py-8 text-center">
            <p>Waiting for events...</p>
            <p className="mt-1 text-green-900">WebSocket events will appear here in real-time</p>
          </div>
        ) : (
          filtered.map(entry => (
            <div key={entry.id} className="flex gap-2 py-0.5 hover:bg-green-950/20 leading-5">
              <span className="text-green-700 shrink-0 select-none">
                {formatTime(entry.timestamp)}
              </span>
              <span className={`inline-flex px-1.5 py-0 text-[10px] font-medium rounded border shrink-0 leading-5 ${getEventBadgeClass(entry.eventType)}`}>
                {entry.eventType}
              </span>
              <span className="text-green-400 break-all whitespace-pre-wrap">
                {truncatePayload(entry.payload)}
              </span>
            </div>
          ))
        )}

        {/* Auto-scroll indicator */}
        {!autoScroll && entries.length > 0 && (
          <button
            onClick={() => {
              setAutoScroll(true)
              if (scrollRef.current) {
                scrollRef.current.scrollTop = scrollRef.current.scrollHeight
              }
            }}
            className="fixed bottom-6 right-6 px-3 py-1.5 bg-green-600/80 text-white text-xs rounded-full shadow-lg hover:bg-green-600 transition-colors z-50"
          >
            Scroll to bottom
          </button>
        )}
      </div>
    </div>
  )
}
