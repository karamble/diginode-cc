import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import api from '../api/client'

interface NodeRow {
  id: string
  nodeNum: number
  nodeType?: string  // "gotailme" | "antihunter" | "gatesensor" | ""
  name: string
  shortName?: string
  ahShortId?: string
  hwModel?: string
  macAddr?: string
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
  temperatureUpdatedAt?: string
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
  telemetryUpdatedAt?: string
}

// timeAgo returns a compact relative string like "4m ago" / "2h ago".
// Treat missing, unparseable, or pre-2001 timestamps as unknown — the backend
// may emit Go's zero time ("0001-01-01T00:00:00Z") for nodes the radio knows
// about but never heard in this session.
function timeAgo(iso?: string): string {
  if (!iso) return '-'
  const t = new Date(iso).getTime()
  if (!isFinite(t) || t < 978307200000) return '-' // before 2001-01-01
  const diff = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

function nodeTypeBadge(nodeType?: string): { label: string; color: string } | null {
  if (nodeType === 'gotailme') return { label: 'GTM', color: 'bg-blue-500/20 text-blue-400 border-blue-500/30' }
  if (nodeType === 'antihunter') return { label: 'AH', color: 'bg-orange-500/20 text-orange-400 border-orange-500/30' }
  if (nodeType === 'gatesensor') return { label: 'GATE', color: 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30' }
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

export default function NodesPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  // resolveTarget returns the @TARGET string the firmware / UI expects.
  // AntiHunter sensors only honour their CONFIG_NODEID (AH34) — Meshtastic
  // short names are dropped — so prefer ahShortId when present, fall back to
  // @NODE_<shortName|nodeNum> for gotailme gateways.
  function resolveTarget(n: NodeRow): string {
    if (n.nodeType === 'antihunter' && n.ahShortId) return `@${n.ahShortId}`
    return `@NODE_${n.shortName || n.nodeNum}`
  }

  const { data: nodes = [], isLoading, error } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.get<NodeRow[]>('/nodes'),
    refetchInterval: 5000,
  })

  const refreshNodes = useMutation({
    mutationFn: () => api.post('/serial/refresh'),
    onSuccess: () => {
      // Wait a moment for the config dump to complete, then refetch
      setTimeout(() => queryClient.invalidateQueries({ queryKey: ['nodes'] }), 2000)
    },
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
                          {[n.ahShortId || n.shortName || n.id, n.nodeType].filter(Boolean).join(' · ')}
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
                          <div>
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
                            {n.telemetryUpdatedAt && (
                              <div className="text-[9px] text-dark-600 font-mono mt-0.5">
                                {timeAgo(n.telemetryUpdatedAt)}
                              </div>
                            )}
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
                          <div className="space-y-4">
                            {/* Identity */}
                            <div>
                              <div className="text-dark-500 text-[10px] uppercase tracking-wider mb-1.5">Identity</div>
                              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                                <div>
                                  <span className="text-dark-500 block">Long Name</span>
                                  <span className="text-dark-300">{n.name || '-'}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Short Name</span>
                                  <span className="text-dark-300 font-mono">{n.shortName || '-'}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">AH Node ID</span>
                                  <span className="text-dark-300 font-mono">{n.ahShortId || '-'}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Node Type</span>
                                  <span className="text-dark-300">
                                    {n.nodeType === 'antihunter' ? 'AntiHunter sensor' : n.nodeType === 'gatesensor' ? 'Gate sensor' : n.nodeType === 'gotailme' ? 'gotailme C2 gateway' : 'unclassified'}
                                    {n.isLocal && <span className="ml-1 text-dark-500">(local)</span>}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Mesh Node ID</span>
                                  <span className="text-dark-300 font-mono">{n.id}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Node Number</span>
                                  <span className="text-dark-300 font-mono">{n.nodeNum}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">MAC Address</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.macAddr ? n.macAddr.toUpperCase() : '-'}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Online</span>
                                  <span className={n.isOnline ? 'text-green-400' : 'text-dark-500'}>
                                    {n.isOnline ? 'yes' : 'no'}
                                  </span>
                                </div>
                              </div>
                            </div>

                            {/* Hardware / Firmware */}
                            <div>
                              <div className="text-dark-500 text-[10px] uppercase tracking-wider mb-1.5">Hardware</div>
                              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                                <div>
                                  <span className="text-dark-500 block">Model</span>
                                  <span className="text-dark-300">{n.hwModel || '-'}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Role</span>
                                  <span className="text-dark-300">{n.role || '-'}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Firmware</span>
                                  <span className="text-dark-300 font-mono">{n.firmwareVersion || '-'}</span>
                                </div>
                              </div>
                            </div>

                            {/* Telemetry */}
                            <div>
                              <div className="text-dark-500 text-[10px] uppercase tracking-wider mb-1.5">
                                Telemetry
                                {n.telemetryUpdatedAt && (
                                  <span className="ml-2 text-dark-600 normal-case">· updated {timeAgo(n.telemetryUpdatedAt)}</span>
                                )}
                              </div>
                              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                                <div>
                                  <span className="text-dark-500 block">Battery</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.batteryLevel && n.batteryLevel > 0 ? `${n.batteryLevel}%` : '-'}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Voltage</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.voltage && n.voltage > 0.1 ? `${n.voltage.toFixed(2)} V` : '-'}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">
                                    Temperature
                                    {n.temperatureUpdatedAt && (
                                      <span className="text-dark-600 normal-case"> · {timeAgo(n.temperatureUpdatedAt)}</span>
                                    )}
                                  </span>
                                  <span className="text-dark-300 font-mono">
                                    {n.temperatureC ? `${n.temperatureC.toFixed(1)}°C` : n.temperature ? `${n.temperature.toFixed(1)}°C` : '-'}
                                    {n.temperatureF ? ` / ${n.temperatureF.toFixed(1)}°F` : ''}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Signal</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.rssi !== undefined && n.rssi !== 0 ? `${n.rssi} dBm` : '-'}
                                    {n.snr !== undefined && n.snr !== 0 ? ` / ${n.snr.toFixed(1)} dB SNR` : ''}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Channel Util</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.channelUtilization !== undefined ? `${n.channelUtilization.toFixed(1)}%` : '-'}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Air TX</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.airUtilTx !== undefined ? `${n.airUtilTx.toFixed(2)}%` : '-'}
                                  </span>
                                </div>
                              </div>
                            </div>

                            {/* Location */}
                            <div>
                              <div className="text-dark-500 text-[10px] uppercase tracking-wider mb-1.5">Location</div>
                              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                                <div>
                                  <span className="text-dark-500 block">Coordinates</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.lat && n.lon ? `${n.lat.toFixed(5)}, ${n.lon.toFixed(5)}` : 'No position'}
                                  </span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Altitude</span>
                                  <span className="text-dark-300 font-mono">
                                    {n.altitude ? `${n.altitude.toFixed(0)} m` : '-'}
                                  </span>
                                </div>
                                {n.siteName && (
                                  <div>
                                    <span className="text-dark-500 block">Site</span>
                                    <span className="text-dark-300" style={n.siteColor ? { color: n.siteColor } : undefined}>
                                      {n.siteName}
                                    </span>
                                  </div>
                                )}
                              </div>
                            </div>

                            {/* Activity */}
                            <div>
                              <div className="text-dark-500 text-[10px] uppercase tracking-wider mb-1.5">Activity</div>
                              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                                <div>
                                  <span className="text-dark-500 block">Last Heard</span>
                                  <span className="text-dark-300">{timeAgo(n.lastHeard)}</span>
                                </div>
                                <div>
                                  <span className="text-dark-500 block">Last Seen</span>
                                  <span className="text-dark-300">{timeAgo(n.lastSeen)}</span>
                                </div>
                              </div>
                              {n.lastMessage && (
                                <div className="mt-2">
                                  <span className="text-dark-500 block text-xs">Last Message</span>
                                  <span className="text-dark-300 font-mono text-xs break-all">{n.lastMessage}</span>
                                </div>
                              )}
                            </div>
                          </div>
                          <div className="mt-3 pt-3 border-t border-dark-700/30 flex gap-2 flex-wrap">
                            <button
                              onClick={(e) => {
                                e.stopPropagation()
                                const target = resolveTarget(n)
                                navigate(`/commands?target=${encodeURIComponent(target)}`)
                              }}
                              className="px-3 py-1 text-xs rounded bg-primary-600/20 text-primary-300 hover:bg-primary-600/30 hover:text-primary-200 transition-colors border border-primary-600/40"
                              title={`Open Commands page with ${resolveTarget(n)} pre-selected`}
                            >
                              Send Command → {resolveTarget(n)}
                            </button>
                            <button
                              onClick={(e) => {
                                e.stopPropagation()
                                refreshNodes.mutate()
                              }}
                              disabled={refreshNodes.isPending}
                              className="px-3 py-1 text-xs rounded bg-dark-700 text-dark-300 hover:bg-dark-600 hover:text-dark-200 disabled:opacity-40 disabled:cursor-not-allowed transition-colors border border-dark-600/50"
                            >
                              {refreshNodes.isPending ? 'Refreshing...' : 'Update Telemetry'}
                            </button>
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
