import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

type ConfigMap = Record<string, unknown>

// Category groupings for config keys
const categories: Record<string, string[]> = {
  serial: ['protocol', 'ackTimeoutMs', 'resultTimeoutMs', 'maxRetries', 'perNodeCmdRate', 'globalCmdRate'],
  detection: ['detectMode', 'detectChannels', 'detectScanSecs', 'allowForever', 'baselineSecs', 'deviceScanSecs', 'droneSecs', 'deauthSecs', 'randomizeSecs'],
  map: ['mapTileUrl', 'mapAttribution', 'minZoom', 'maxZoom', 'defaultRadiusM'],
  retention: ['nodePosRetentionDays', 'commandRetentionDays', 'auditRetentionDays'],
  system: ['appName', 'timezone', 'env', 'logLevel', 'structuredLogs', 'metricsEnabled', 'metricsPath', 'healthEnabled'],
  auth: ['invitationExpiryHours', 'passwordResetExpiryHours'],
}

function getCategoryForKey(key: string): string {
  for (const [cat, keys] of Object.entries(categories)) {
    if (keys.includes(key)) return cat
  }
  return 'other'
}

const categoryLabels: Record<string, string> = {
  serial: 'Serial / Protocol',
  detection: 'Detection',
  map: 'Map / Geo',
  retention: 'Data Retention',
  system: 'System',
  auth: 'Authentication',
  other: 'Other',
}

const categoryOrder = ['system', 'serial', 'detection', 'map', 'retention', 'auth', 'other']

export default function ConfigPage() {
  const queryClient = useQueryClient()
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')

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

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-dark-100">Configuration</h2>
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
        <div className="space-y-6">
          {categoryOrder
            .filter((cat) => grouped.has(cat))
            .map((cat) => (
              <div key={cat} className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
                <div className="px-4 py-3 border-b border-dark-700/50">
                  <h3 className="text-sm font-medium text-dark-200">{categoryLabels[cat] || cat}</h3>
                </div>
                <div className="divide-y divide-dark-700/30">
                  {grouped.get(cat)!.sort((a, b) => a[0].localeCompare(b[0])).map(([key, value]) => (
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
                            <span className="text-sm font-mono text-dark-200 max-w-md truncate">
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
              </div>
            ))}
          {updateMutation.isError && (
            <p className="text-sm text-red-400">Failed to update: {(updateMutation.error as Error).message}</p>
          )}
        </div>
      )}
    </div>
  )
}
