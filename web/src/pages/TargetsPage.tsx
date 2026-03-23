import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface Target {
  id: string
  name: string
  description?: string
  targetType?: string
  mac?: string
  latitude?: number
  longitude?: number
  status: string
  createdAt: string
  updatedAt: string
}

const statusBadge: Record<string, string> = {
  active: 'bg-green-600/20 text-green-400 border-green-500/30',
  resolved: 'bg-dark-600/20 text-dark-400 border-dark-500/30',
}

export default function TargetsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [newTarget, setNewTarget] = useState({ name: '', mac: '', targetType: 'wifi', description: '', latitude: '', longitude: '' })

  const { data: targets, isLoading, error } = useQuery<Target[]>({
    queryKey: ['targets'],
    queryFn: () => api.get('/targets'),
  })

  const createMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.post('/targets', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['targets'] })
      setShowCreate(false)
      setEditingId(null)
      setNewTarget({ name: '', mac: '', targetType: 'wifi', description: '', latitude: '', longitude: '' })
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) => api.put(`/targets/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['targets'] })
      setEditingId(null)
      setNewTarget({ name: '', mac: '', targetType: 'wifi', description: '', latitude: '', longitude: '' })
    },
  })

  const startEdit = (t: Target) => {
    setEditingId(t.id)
    setShowCreate(true)
    setNewTarget({
      name: t.name,
      mac: t.mac || '',
      targetType: t.targetType || 'wifi',
      description: t.description || '',
      latitude: t.latitude ? String(t.latitude) : '',
      longitude: t.longitude ? String(t.longitude) : '',
    })
  }

  const handleSave = () => {
    const body: Record<string, unknown> = {
      name: newTarget.name,
      mac: newTarget.mac || undefined,
      targetType: newTarget.targetType,
      description: newTarget.description || undefined,
      status: 'active',
    }
    if (newTarget.latitude) body.latitude = parseFloat(newTarget.latitude)
    if (newTarget.longitude) body.longitude = parseFloat(newTarget.longitude)

    if (editingId) {
      updateMutation.mutate({ id: editingId, body })
    } else {
      createMutation.mutate(body)
    }
  }

  const resolveMutation = useMutation({
    mutationFn: (id: string) => api.post(`/targets/${id}/resolve`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['targets'] }),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/targets/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['targets'] }),
  })

  const clearMutation = useMutation({
    mutationFn: () => api.post('/targets/clear'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['targets'] }),
  })

  const formatDate = (dateStr: string) => {
    const d = new Date(dateStr)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  const formatCoord = (val?: number) => {
    if (!val || val === 0) return '-'
    return val.toFixed(6)
  }

  // Sort: active first, then by updatedAt descending
  const sorted = targets ? [...targets].sort((a, b) => {
    if (a.status !== b.status) return a.status === 'active' ? -1 : 1
    return new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
  }) : []

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Targets</h2>
          <p className="text-sm text-dark-400 mt-1">
            {sorted.filter(t => t.status === 'active').length} active / {sorted.length} total
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => {
              if (confirm('Clear all targets?')) clearMutation.mutate()
            }}
            className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors"
          >
            Clear All
          </button>
          <button
            onClick={() => {
              if (showCreate) {
                setShowCreate(false)
                setEditingId(null)
                setNewTarget({ name: '', mac: '', targetType: 'wifi', description: '', latitude: '', longitude: '' })
              } else {
                setShowCreate(true)
              }
            }}
            className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
          >
            {showCreate ? 'Cancel' : 'Add Target'}
          </button>
        </div>
      </div>

      {showCreate && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <h3 className="text-sm font-medium text-dark-200 mb-3">{editingId ? 'Edit Target' : 'Create Target'}</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            <input
              type="text"
              placeholder="Name *"
              value={newTarget.name}
              onChange={(e) => setNewTarget({ ...newTarget, name: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <input
              type="text"
              placeholder="MAC Address"
              value={newTarget.mac}
              onChange={(e) => setNewTarget({ ...newTarget, mac: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <select
              value={newTarget.targetType}
              onChange={(e) => setNewTarget({ ...newTarget, targetType: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            >
              <option value="wifi">WiFi</option>
              <option value="ble">BLE</option>
              <option value="drone">Drone</option>
              <option value="vehicle">Vehicle</option>
              <option value="person">Person</option>
            </select>
            <input
              type="text"
              placeholder="Description"
              value={newTarget.description}
              onChange={(e) => setNewTarget({ ...newTarget, description: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <input
              type="text"
              placeholder="Latitude"
              value={newTarget.latitude}
              onChange={(e) => setNewTarget({ ...newTarget, latitude: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <input
              type="text"
              placeholder="Longitude"
              value={newTarget.longitude}
              onChange={(e) => setNewTarget({ ...newTarget, longitude: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
          </div>
          <div className="flex items-center gap-2 mt-3">
            <button
              onClick={handleSave}
              disabled={!newTarget.name || createMutation.isPending || updateMutation.isPending}
              className="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
            >
              {editingId ? 'Save' : 'Create'}
            </button>
            {(createMutation.isError || updateMutation.isError) && (
              <span className="text-sm text-red-400">{((createMutation.error || updateMutation.error) as Error)?.message}</span>
            )}
          </div>
        </div>
      )}

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading targets...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load targets: {(error as Error).message}</p>
          </div>
        ) : sorted.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">No targets tracked</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Name</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Type</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">MAC</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Status</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Lat</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Lon</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Created</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {sorted.map((t) => (
                  <tr key={t.id} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3">
                      <div className="text-sm text-dark-200 font-medium">{t.name}</div>
                      {t.description && (
                        <div className="text-xs text-dark-500 mt-0.5 truncate max-w-[200px]">{t.description}</div>
                      )}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300">{t.targetType || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-300 font-mono">{t.mac || '-'}</td>
                    <td className="px-4 py-3">
                      <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${statusBadge[t.status] || statusBadge.active}`}>
                        {t.status}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400 font-mono">{formatCoord(t.latitude)}</td>
                    <td className="px-4 py-3 text-sm text-dark-400 font-mono">{formatCoord(t.longitude)}</td>
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(t.createdAt)}</td>
                    <td className="px-4 py-3 text-right space-x-3">
                      <button
                        onClick={() => startEdit(t)}
                        className="text-xs text-primary-400 hover:text-primary-300 transition-colors"
                      >
                        Edit
                      </button>
                      {t.status === 'active' && (
                        <button
                          onClick={() => resolveMutation.mutate(t.id)}
                          className="text-xs text-yellow-400 hover:text-yellow-300 transition-colors"
                        >
                          Resolve
                        </button>
                      )}
                      <button
                        onClick={() => {
                          if (confirm(`Delete target "${t.name}"?`)) deleteMutation.mutate(t.id)
                        }}
                        className="text-xs text-red-400 hover:text-red-300 transition-colors"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
