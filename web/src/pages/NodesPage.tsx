import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface NodeRow {
  id: string
  nodeNum: number
  nodeType?: string  // "gotailme" | "antihunter" | ""
  name: string
  shortName?: string
  hwModel?: string
  role?: string
  firmwareVersion?: string
  lat?: number
  lon?: number
  altitude?: number
  batteryLevel?: number
  voltage?: number
  channelUtilization?: number
  airUtilTx?: number
  temperature?: number
  temperatureC?: number
  temperatureF?: number
  snr?: number
  rssi?: number
  lastHeard?: string
  lastSeen?: string
  isOnline: boolean
  isLocal?: boolean
  siteId?: string
  siteName?: string
  siteColor?: string
  lastMessage?: string
}

function nodeTypeBadge(nodeType?: string): { label: string; color: string } | null {
  if (nodeType === 'gotailme') return { label: 'GTM', color: 'bg-blue-500/20 text-blue-400 border-blue-500/30' }
  if (nodeType === 'antihunter') return { label: 'AH', color: 'bg-orange-500/20 text-orange-400 border-orange-500/30' }
  return null
}

function batteryColor(level: number | undefined): string {
  if (level === undefined || level === 0) return 'bg-dark-600'
  if (level > 60) return 'bg-status-friendly'
  if (level > 25) return 'bg-alert-alert'
  return 'bg-status-hostile'
}

function batteryTextColor(level: number | undefined): string {
  if (level === undefined || level === 0) return 'text-dark-500'
  if (level > 60) return 'text-status-friendly'
  if (level > 25) return 'text-alert-alert'
  return 'text-status-hostile'
}

function signalStrength(rssi: number | undefined): { label: string; color: string } {
  if (rssi === undefined || rssi === 0) return { label: '-', color: 'text-dark-500' }
  if (rssi > -70) return { label: 'Strong', color: 'text-status-friendly' }
  if (rssi > -100) return { label: 'Fair', color: 'text-alert-alert' }
  return { label: 'Weak', color: 'text-status-hostile' }
}

function timeAgo(ts: string | undefined): string {
  if (!ts) return '-'
  const diff = Date.now() - new Date(ts).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86400_000) return `${Math.floor(diff / 3600_000)}h ago`
  return `${Math.floor(diff / 86400_000)}d ago`
}

export default function NodesPage() {
  const queryClient = useQueryClient()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const { data: nodes = [], isLoading, error } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.get<NodeRow[]>('/nodes'),
    refetchInterval: 5000,
  })

  const deleteNode = useMutation({
    mutationFn: (nodeNum: number) => api.delete(`/nodes/${nodeNum}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['nodes'] }),
  })

  const clearAll = useMutation({
    mutationFn: () => api.post('/nodes/clear'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['nodes'] }),
  })

  const onlineCount = nodes.filter((n: NodeRow) => n.isOnline).length
  const offlineCount = nodes.length - onlineCount

  return (
    <div className="p-6 space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">
            Mesh Nodes
            <span className="ml-2 text-sm font-normal text-dark-400">({nodes.length})</span>
          </h2>
          <div className="flex gap-3 mt-1 text-xs">
            <span className="text-status-friendly">{onlineCount} online</span>
            {offlineCount > 0 && <span className="text-dark-500">{offlineCount} offline</span>}
          </div>
        </div>
        <button
          onClick={() => {
            if (nodes.length > 0 && confirm('Clear all tracked nodes?')) clearAll.mutate()
          }}
          disabled={nodes.length === 0}
          className="px-3 py-1.5 text-xs rounded-lg bg-dark-800 text-dark-400 hover:bg-dark-700 hover:text-dark-200 disabled:opacity-40 disabled:cursor-not-allowed transition-colors border border-dark-700/50"
        >
          Clear All
        </button>
      </div>

      {/* Error banner */}
      {error && (
        <div className="px-4 py-2 rounded-lg bg-status-hostile/10 border border-status-hostile/30 text-status-hostile text-sm">
          Failed to load nodes: {(error as Error).message}
        </div>
      )}

      {/* Table */}
      <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-dark-700/50 text-dark-400 text-xs uppercase tracking-wider">
              <th className="text-center px-3 py-3 w-8"></th>
              <th className="text-left px-4 py-3">Node</th>
              <th className="text-left px-4 py-3">Hardware</th>
              <th className="text-left px-4 py-3">Battery</th>
              <th className="text-right px-4 py-3">Voltage</th>
              <th className="text-right px-4 py-3">SNR</th>
              <th className="text-right px-4 py-3">RSSI</th>
              <th className="text-left px-4 py-3">Site</th>
              <th className="text-left px-4 py-3">Last Heard</th>
              <th className="text-right px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td colSpan={10} className="px-4 py-12 text-center text-dark-500">
                  <div className="flex items-center justify-center gap-2">
                    <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                    Loading nodes...
                  </div>
                </td>
              </tr>
            ) : nodes.length === 0 ? (
              <tr>
                <td colSpan={10} className="px-4 py-12 text-center text-dark-500">
                  No mesh nodes detected
                </td>
              </tr>
            ) : (
              nodes.map((n: NodeRow) => {
                const sig = signalStrength(n.rssi)
                const badge = nodeTypeBadge(n.nodeType)
                return (
                  <>
                    <tr
                      key={n.id}
                      className={`border-b border-dark-700/30 hover:bg-dark-800/30 cursor-pointer transition-colors ${
                        n.isLocal ? 'bg-dark-800/40' : ''
                      }`}
                      onClick={() => setExpandedId(expandedId === n.id ? null : n.id)}
                    >
                      {/* Online indicator */}
                      <td className="text-center px-3 py-2.5">
                        <span
                          className={`inline-block w-2.5 h-2.5 rounded-full ${
                            n.isOnline ? 'bg-status-friendly shadow-[0_0_6px_rgba(34,197,94,0.4)]' : 'bg-dark-600'
                          }`}
                          title={n.isOnline ? 'Online' : 'Offline'}
                        />
                      </td>

                      {/* Node name + type badge */}
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2">
                          <span className="text-dark-200 font-medium text-sm">
                            {n.name || n.shortName || `!${n.nodeNum.toString(16)}`}
                          </span>
                          {badge && (
                            <span className={`px-1.5 py-0.5 text-[10px] font-mono rounded border ${badge.color}`}>
                              {badge.label}
                            </span>
                          )}
                          {n.isLocal && (
                            <span className="px-1.5 py-0.5 text-[10px] font-mono rounded border bg-dark-600/50 text-dark-300 border-dark-500/30">
                              LOCAL
                            </span>
                          )}
                        </div>
                        <div className="text-dark-500 text-xs font-mono mt-0.5">
                          {n.shortName ? `${n.shortName} / ` : ''}{n.id}
                        </div>
                      </td>

                      {/* Hardware */}
                      <td className="px-4 py-2.5 text-dark-400 text-xs">
                        {n.hwModel || '-'}
                        {n.role && <span className="ml-1 text-dark-500">({n.role})</span>}
                      </td>

                      {/* Battery bar */}
                      <td className="px-4 py-2.5">
                        {n.batteryLevel && n.batteryLevel > 0 ? (
                          <div className="flex items-center gap-2">
                            <div className="w-16 h-2 bg-dark-700 rounded-full overflow-hidden">
                              <div
                                className={`h-full rounded-full ${batteryColor(n.batteryLevel)}`}
                                style={{ width: `${Math.min(n.batteryLevel, 100)}%` }}
                              />
                            </div>
                            <span className={`text-xs font-mono ${batteryTextColor(n.batteryLevel)}`}>
                              {n.batteryLevel}%
                            </span>
                          </div>
                        ) : (
                          <span className="text-dark-500 text-xs">-</span>
                        )}
                      </td>

                      {/* Voltage */}
                      <td className="px-4 py-2.5 text-right font-mono text-dark-300 text-xs">
                        {n.voltage ? `${n.voltage.toFixed(2)}V` : '-'}
                      </td>

                      {/* SNR */}
                      <td className="px-4 py-2.5 text-right font-mono text-dark-300 text-xs">
                        {n.snr?.toFixed(1) ?? '-'}
                      </td>

                      {/* RSSI with signal label */}
                      <td className="px-4 py-2.5 text-right">
                        <span className="font-mono text-dark-300 text-xs">{n.rssi ?? '-'}</span>
                        {n.rssi !== undefined && n.rssi !== 0 && (
                          <span className={`ml-1 text-[10px] ${sig.color}`}>{sig.label}</span>
                        )}
                      </td>

                      {/* Site */}
                      <td className="px-4 py-2.5 text-dark-400 text-xs">
                        {n.siteName ? (
                          <span className="flex items-center gap-1.5">
                            {n.siteColor && (
                              <span
                                className="inline-block w-2 h-2 rounded-full"
                                style={{ backgroundColor: n.siteColor }}
                              />
                            )}
                            {n.siteName}
                          </span>
                        ) : '-'}
                      </td>

                      {/* Last heard */}
                      <td className="px-4 py-2.5 text-dark-500 text-xs">
                        {timeAgo(n.lastHeard || n.lastSeen)}
                      </td>

                      {/* Actions */}
                      <td className="px-4 py-2.5 text-right">
                        <button
                          onClick={(e) => {
                            e.stopPropagation()
                            if (confirm(`Remove node ${n.name || n.id}?`)) {
                              deleteNode.mutate(n.nodeNum)
                            }
                          }}
                          className="text-dark-500 hover:text-status-hostile transition-colors"
                          title="Remove node"
                        >
                          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                          </svg>
                        </button>
                      </td>
                    </tr>

                    {/* Expanded detail row */}
                    {expandedId === n.id && (
                      <tr key={`${n.id}-detail`} className="border-b border-dark-700/30 bg-dark-800/20">
                        <td colSpan={10} className="px-6 py-4">
                          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-xs">
                            <div>
                              <span className="text-dark-500 block">Position</span>
                              <span className="text-dark-300 font-mono">
                                {n.lat && n.lon ? `${n.lat.toFixed(5)}, ${n.lon.toFixed(5)}` : 'No position'}
                              </span>
                            </div>
                            <div>
                              <span className="text-dark-500 block">Altitude</span>
                              <span className="text-dark-300 font-mono">
                                {n.altitude ? `${n.altitude.toFixed(0)}m` : '-'}
                              </span>
                            </div>
                            <div>
                              <span className="text-dark-500 block">Channel Util / Air TX</span>
                              <span className="text-dark-300 font-mono">
                                {n.channelUtilization?.toFixed(1) ?? '-'}% / {n.airUtilTx?.toFixed(1) ?? '-'}%
                              </span>
                            </div>
                            <div>
                              <span className="text-dark-500 block">Temperature</span>
                              <span className="text-dark-300 font-mono">
                                {n.temperatureC ? `${n.temperatureC.toFixed(1)}C` : n.temperature ? `${n.temperature.toFixed(1)}C` : '-'}
                                {n.temperatureF ? ` / ${n.temperatureF.toFixed(1)}F` : ''}
                              </span>
                            </div>
                            <div>
                              <span className="text-dark-500 block">Firmware</span>
                              <span className="text-dark-300">{n.firmwareVersion || '-'}</span>
                            </div>
                            <div>
                              <span className="text-dark-500 block">Node Number</span>
                              <span className="text-dark-300 font-mono">{n.nodeNum}</span>
                            </div>
                            {n.lastMessage && (
                              <div className="col-span-2">
                                <span className="text-dark-500 block">Last Message</span>
                                <span className="text-dark-300">{n.lastMessage}</span>
                              </div>
                            )}
                          </div>
                        </td>
                      </tr>
                    )}
                  </>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
