import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useCallback } from 'react'
import api from '../api/client'

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
}

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
  other: 'O',
}

const categoryOrder = ['system', 'serial', 'detection', 'map', 'retention', 'auth', 'rateLimit', 'other']

export default function ConfigPage() {
  const queryClient = useQueryClient()
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')
  const [collapsedSections, setCollapsedSections] = useState<Record<string, boolean>>({})

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
                            {editingKey === key ? (
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
    </div>
  )
}
