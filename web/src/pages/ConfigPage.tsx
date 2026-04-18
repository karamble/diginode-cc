import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useCallback } from 'react'
import api from '../api/client'
import { useAuthStore } from '../stores/authStore'

type ConfigMap = Record<string, unknown>

// Category groupings for config keys
const categories: Record<string, string[]> = {
  system: ['appName', 'timezone', 'env', 'logLevel', 'structuredLogs', 'metricsEnabled', 'metricsPath', 'healthEnabled'],
  serial: ['protocol', 'ackTimeoutMs', 'resultTimeoutMs', 'maxRetries'],
  detection: ['detectMode', 'detectChannels', 'detectScanSecs', 'allowForever', 'baselineSecs', 'deviceScanSecs', 'droneSecs', 'deauthSecs', 'randomizeSecs'],
  map: ['mapTileUrl', 'mapAttribution', 'minZoom', 'maxZoom', 'defaultRadiusM'],
  retention: ['nodePosRetentionDays', 'commandRetentionDays', 'auditRetentionDays'],
  auth: ['invitationExpiryHours', 'passwordResetExpiryHours'],
  rateLimit: ['perNodeCmdRate', 'globalCmdRate'],
  meshBroadcast: ['gpsBroadcastEnabled', 'statusBroadcastEnabled', 'statusBroadcastIntervalSecs'],
}

// Keys that should render as an instant toggle switch instead of the generic
// text input. Flipping the switch writes immediately (no Save button). The
// backend fires a hardware side-effect when gpsBroadcastEnabled changes.
const toggleKeys = new Set<string>(['gpsBroadcastEnabled', 'statusBroadcastEnabled'])

// Keys that render as a clamped number input with Save/Cancel.
const intervalKeys = new Set<string>(['statusBroadcastIntervalSecs'])

function getCategoryForKey(key: string): string {
  for (const [cat, keys] of Object.entries(categories)) {
    if (keys.includes(key)) return cat
  }
  return 'other'
}

const categoryLabels: Record<string, string> = {
  system: 'System',
  serial: 'Serial / Protocol',
  detection: 'Detection',
  map: 'Map / Geo',
  retention: 'Data Retention',
  auth: 'Authentication',
  rateLimit: 'Command Rate Limiting',
  meshBroadcast: 'Mesh Broadcast',
  other: 'Other',
}

const categoryDescriptions: Record<string, string> = {
  system: 'Application name, environment, logging, and metrics configuration',
  serial: 'Serial protocol settings, timeouts, and retry behavior',
  detection: 'Scan modes, channel configuration, and detection timing',
  map: 'Map tile source, attribution, zoom levels, and default radius',
  retention: 'How long to keep node positions, commands, and audit logs',
  auth: 'Invitation and password reset token expiry periods',
  rateLimit: 'Per-node and global command rate limits',
  meshBroadcast: 'Periodic STATUS heartbeat over LoRa mesh + GPS visibility (mirrored with gotailme position toggle)',
  other: 'Additional configuration settings',
}

const categoryIcons: Record<string, string> = {
  system: 'S',
  serial: 'P',
  detection: 'D',
  map: 'M',
  retention: 'R',
  auth: 'A',
  rateLimit: 'L',
  meshBroadcast: 'B',
  other: 'O',
}

const categoryOrder = ['system', 'serial', 'detection', 'map', 'retention', 'auth', 'rateLimit', 'meshBroadcast', 'other']

export default function ConfigPage() {
  const queryClient = useQueryClient()
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')
  const [collapsedSections, setCollapsedSections] = useState<Record<string, boolean>>({})
  const [pruneDays, setPruneDays] = useState('30')
  const [pruneResult, setPruneResult] = useState<string | null>(null)

  const { data: config, isLoading, error } = useQuery<ConfigMap>({
    queryKey: ['config'],
    queryFn: () => api.get('/config'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ key, value }: { key: string; value: unknown }) =>
      api.put(`/config/${key}`, { value }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['config'] })
      setEditingKey(null)
      setEditValue('')
    },
  })

  const toggleSection = useCallback((cat: string) => {
    setCollapsedSections((prev) => ({ ...prev, [cat]: !prev[cat] }))
  }, [])

  const startEdit = (key: string, value: unknown) => {
    setEditingKey(key)
    setEditValue(typeof value === 'string' ? value : JSON.stringify(value))
  }

  const saveEdit = (key: string) => {
    let parsed: unknown
    try {
      parsed = JSON.parse(editValue)
    } catch {
      parsed = editValue
    }
    updateMutation.mutate({ key, value: parsed })
  }

  const cancelEdit = () => {
    setEditingKey(null)
    setEditValue('')
  }

  // Group config by category
  const grouped = new Map<string, [string, unknown][]>()
  if (config) {
    for (const [key, value] of Object.entries(config)) {
      const cat = getCategoryForKey(key)
      if (!grouped.has(cat)) grouped.set(cat, [])
      grouped.get(cat)!.push([key, value])
    }
  }

  const formatValue = (value: unknown): string => {
    if (typeof value === 'boolean') return value ? 'true' : 'false'
    if (typeof value === 'string') return value || '(empty)'
    return JSON.stringify(value)
  }

  const getValueBadge = (value: unknown): string | null => {
    if (typeof value === 'boolean') {
      return value ? 'text-green-400' : 'text-dark-500'
    }
    if (typeof value === 'number') return 'text-amber-400'
    return null
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Configuration</h2>
          <p className="text-sm text-dark-400 mt-1">
            {config ? `${Object.keys(config).length} settings across ${grouped.size} sections` : 'Loading...'}
          </p>
        </div>
      </div>

      {isLoading ? (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-8 text-center">
          <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
          <p className="mt-2 text-sm text-dark-400">Loading configuration...</p>
        </div>
      ) : error ? (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-8 text-center">
          <p className="text-sm text-red-400">Failed to load config: {(error as Error).message}</p>
        </div>
      ) : !config || Object.keys(config).length === 0 ? (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-8 text-center">
          <p className="text-sm text-dark-400">No configuration entries found</p>
        </div>
      ) : (
        <div className="space-y-4">
          {categoryOrder
            .filter((cat) => grouped.has(cat))
            .map((cat) => {
              const isCollapsed = collapsedSections[cat] ?? false
              const entries = grouped.get(cat)!.sort((a, b) => a[0].localeCompare(b[0]))
              return (
                <div key={cat} className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
                  {/* Collapsible Header */}
                  <button
                    onClick={() => toggleSection(cat)}
                    className="w-full flex items-center justify-between px-4 py-3 border-b border-dark-700/50 hover:bg-dark-800/30 transition-colors text-left"
                  >
                    <div className="flex items-center gap-3">
                      <div className="w-8 h-8 rounded-lg bg-primary-600/10 border border-primary-500/20 flex items-center justify-center text-primary-400 text-xs font-bold">
                        {categoryIcons[cat] || '?'}
                      </div>
                      <div>
                        <h3 className="text-sm font-medium text-dark-200">{categoryLabels[cat] || cat}</h3>
                        <p className="text-xs text-dark-500">{categoryDescriptions[cat] || ''}</p>
                      </div>
                    </div>
                    <div className="flex items-center gap-3">
                      <span className="text-xs text-dark-500">{entries.length} settings</span>
                      <svg
                        className={`w-4 h-4 text-dark-400 transition-transform ${isCollapsed ? '' : 'rotate-180'}`}
                        fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                      </svg>
                    </div>
                  </button>

                  {/* Content */}
                  {!isCollapsed && (
                    <div className="divide-y divide-dark-700/30">
                      {entries.map(([key, value]) => (
                        <div key={key} className="flex items-center justify-between px-4 py-3 hover:bg-dark-800/30 transition-colors">
                          <div className="flex-1 min-w-0 mr-4">
                            <span className="text-sm font-mono text-dark-300">{key}</span>
                          </div>
                          <div className="flex items-center gap-2 flex-shrink-0">
                            {toggleKeys.has(key) ? (
                              <button
                                role="switch"
                                aria-checked={Boolean(value)}
                                disabled={updateMutation.isPending}
                                onClick={() => updateMutation.mutate({ key, value: !value })}
                                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors disabled:opacity-50 ${
                                  value ? 'bg-primary-600' : 'bg-dark-700'
                                }`}
                              >
                                <span
                                  className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                                    value ? 'translate-x-6' : 'translate-x-1'
                                  }`}
                                />
                              </button>
                            ) : intervalKeys.has(key) ? (
                              editingKey === key ? (
                                <>
                                  <input
                                    type="number"
                                    min={60}
                                    max={3600}
                                    step={60}
                                    value={editValue}
                                    onChange={(e) => setEditValue(e.target.value)}
                                    onKeyDown={(e) => {
                                      if (e.key === 'Enter') saveEdit(key)
                                      if (e.key === 'Escape') cancelEdit()
                                    }}
                                    className="w-32 px-2 py-1 bg-dark-800 border border-primary-500 rounded text-dark-100 text-sm font-mono focus:outline-none"
                                    autoFocus
                                  />
                                  <span className="text-xs text-dark-500">sec (60-3600)</span>
                                  <button
                                    onClick={() => saveEdit(key)}
                                    disabled={updateMutation.isPending}
                                    className="px-3 py-1 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-xs rounded transition-colors"
                                  >
                                    {updateMutation.isPending ? '...' : 'Save'}
                                  </button>
                                  <button
                                    onClick={cancelEdit}
                                    className="px-3 py-1 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded transition-colors"
                                  >
                                    Cancel
                                  </button>
                                </>
                              ) : (
                                <>
                                  <span className="text-sm font-mono text-amber-400">{formatValue(value)}s</span>
                                  <button
                                    onClick={() => startEdit(key, value)}
                                    className="px-3 py-1 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded transition-colors"
                                  >
                                    Edit
                                  </button>
                                </>
                              )
                            ) : editingKey === key ? (
                              <>
                                <input
                                  type="text"
                                  value={editValue}
                                  onChange={(e) => setEditValue(e.target.value)}
                                  onKeyDown={(e) => {
                                    if (e.key === 'Enter') saveEdit(key)
                                    if (e.key === 'Escape') cancelEdit()
                                  }}
                                  className="w-64 px-2 py-1 bg-dark-800 border border-primary-500 rounded text-dark-100 text-sm font-mono focus:outline-none"
                                  autoFocus
                                />
                                <button
                                  onClick={() => saveEdit(key)}
                                  disabled={updateMutation.isPending}
                                  className="px-3 py-1 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-xs rounded transition-colors"
                                >
                                  {updateMutation.isPending ? '...' : 'Save'}
                                </button>
                                <button
                                  onClick={cancelEdit}
                                  className="px-3 py-1 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded transition-colors"
                                >
                                  Cancel
                                </button>
                              </>
                            ) : (
                              <>
                                <span className={`text-sm font-mono max-w-md truncate ${getValueBadge(value) || 'text-dark-200'}`}>
                                  {formatValue(value)}
                                </span>
                                <button
                                  onClick={() => startEdit(key, value)}
                                  className="px-3 py-1 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded transition-colors"
                                >
                                  Edit
                                </button>
                              </>
                            )}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          {updateMutation.isError && (
            <p className="text-sm text-red-400">Failed to update: {(updateMutation.error as Error).message}</p>
          )}
        </div>
      )}

      {/* Data Management (ADMIN only) */}
      {isAdmin && (
        <div className="mt-8 space-y-4">
          <h2 className="text-lg font-semibold text-dark-100">Data Management</h2>
          <p className="text-sm text-dark-400 -mt-2">Database maintenance operations. These actions are irreversible.</p>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {/* Clear Detection Data */}
            <div className="bg-surface rounded-lg border border-dark-700/50 p-4">
              <h3 className="text-sm font-medium text-dark-200 mb-1">Clear Detection Data</h3>
              <p className="text-xs text-dark-500 mb-3">
                Remove all drones, targets, inventory devices, positions, and detection history.
                Keeps config, users, alert rules, geofences, and webhooks.
              </p>
              <button
                onClick={() => {
                  if (confirm('Clear all detection data? This cannot be undone.')) {
                    api.post('/admin/clear-detections').then(() => {
                      queryClient.invalidateQueries()
                      alert('Detection data cleared.')
                    }).catch((e: Error) => alert('Failed: ' + e.message))
                  }
                }}
                className="px-4 py-2 bg-orange-600/20 hover:bg-orange-600/30 text-orange-400 border border-orange-500/30 text-xs rounded font-medium transition-colors"
              >
                Clear Detections
              </button>
            </div>

            {/* Clear All Operational Data */}
            <div className="bg-surface rounded-lg border border-dark-700/50 p-4">
              <h3 className="text-sm font-medium text-dark-200 mb-1">Clear Operational Data</h3>
              <p className="text-xs text-dark-500 mb-3">
                Remove all detection data plus chat messages, commands, alert events, and audit log.
                Keeps users, config, rules, and geofences.
              </p>
              <button
                onClick={() => {
                  if (confirm('Clear ALL operational data? This cannot be undone.')) {
                    api.post('/admin/clear-operational').then(() => {
                      queryClient.invalidateQueries()
                      alert('Operational data cleared.')
                    }).catch((e: Error) => alert('Failed: ' + e.message))
                  }
                }}
                className="px-4 py-2 bg-orange-600/20 hover:bg-orange-600/30 text-orange-400 border border-orange-500/30 text-xs rounded font-medium transition-colors"
              >
                Clear Operational Data
              </button>
            </div>

            {/* Prune Old Data */}
            <div className="bg-surface rounded-lg border border-dark-700/50 p-4">
              <h3 className="text-sm font-medium text-dark-200 mb-1">Prune Old Data</h3>
              <p className="text-xs text-dark-500 mb-3">
                Delete detection history, positions, chat, commands, alerts, and audit records
                older than the specified number of days.
              </p>
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  value={pruneDays}
                  onChange={e => setPruneDays(e.target.value)}
                  min="1" max="365"
                  className="w-20 px-2 py-2 bg-dark-800 border border-dark-600 rounded text-sm text-dark-200 focus:outline-none focus:border-primary-500"
                />
                <span className="text-xs text-dark-500">days</span>
                <button
                  onClick={() => {
                    const days = parseInt(pruneDays) || 30
                    if (confirm(`Delete all records older than ${days} days?`)) {
                      api.post('/admin/prune', { days }).then((r: unknown) => {
                        const res = r as Record<string, unknown>
                        setPruneResult(`Deleted ${res.deleted} records older than ${days} days`)
                        queryClient.invalidateQueries()
                      }).catch((e: Error) => alert('Failed: ' + e.message))
                    }
                  }}
                  className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded font-medium transition-colors"
                >
                  Prune
                </button>
              </div>
              {pruneResult && <p className="text-xs text-green-400 mt-2">{pruneResult}</p>}
            </div>

            {/* Clear Tile Cache */}
            <div className="bg-surface rounded-lg border border-dark-700/50 p-4">
              <h3 className="text-sm font-medium text-dark-200 mb-1">Clear Tile Cache</h3>
              <p className="text-xs text-dark-500 mb-3">
                Delete all cached map tiles. Tiles will re-download automatically when online.
              </p>
              <button
                onClick={() => {
                  if (confirm('Clear all cached map tiles?')) {
                    api.delete('/admin/tiles-cache').then(() => {
                      alert('Tile cache cleared.')
                    }).catch((e: Error) => alert('Failed: ' + e.message))
                  }
                }}
                className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded font-medium transition-colors"
              >
                Clear Tiles
              </button>
            </div>

            {/* Factory Reset */}
            <div className="bg-surface rounded-lg border border-red-700/30 p-4">
              <h3 className="text-sm font-medium text-red-400 mb-1">Factory Reset</h3>
              <p className="text-xs text-dark-500 mb-3">
                Delete ALL data — users, sites, config, rules, detections, everything.
                Re-seeds the default admin account (admin@example.com / admin).
              </p>
              <button
                onClick={() => {
                  if (confirm('FACTORY RESET: This will delete ALL data and cannot be undone. Continue?')) {
                    if (confirm('Are you absolutely sure? Type OK in the next prompt.')) {
                      api.post('/admin/factory-reset', { confirm: 'FACTORY_RESET' }).then(() => {
                        alert('Factory reset complete. You will be logged out.')
                        localStorage.removeItem('cc_token')
                        window.location.href = '/'
                      }).catch((e: Error) => alert('Failed: ' + e.message))
                    }
                  }
                }}
                className="px-4 py-2 bg-red-600/20 hover:bg-red-600/30 text-red-400 border border-red-500/30 text-xs rounded font-medium transition-colors"
              >
                Factory Reset
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
