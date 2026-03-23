import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo } from 'react'
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
  url?: string
  tags?: string[]
  notes?: string
  createdBy?: string
  firstNodeId?: string
  trackingConfidence?: number | null
  trackingUncertainty?: number | null
  triangulationMethod?: string
  createdAt: string
  updatedAt: string
}

const statusBadge: Record<string, string> = {
  active: 'bg-green-600/20 text-green-400 border-green-500/30',
  triangulating: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
  resolved: 'bg-dark-600/20 text-dark-400 border-dark-500/30',
}

function confidenceBadge(conf: number | null | undefined, unc: number | null | undefined) {
  if (conf == null) return { color: 'bg-dark-600/20 text-dark-500 border-dark-600/30', label: 'No data', quality: '' }
  const pct = Math.round(conf * 100)
  if (conf > 0.7 && (unc == null || unc < 100))
    return { color: 'bg-green-600/20 text-green-400 border-green-500/30', label: `${pct}%`, quality: 'High' }
  if (conf > 0.5 && (unc == null || unc < 300))
    return { color: 'bg-yellow-600/20 text-yellow-400 border-yellow-500/30', label: `${pct}%`, quality: 'Medium' }
  return { color: 'bg-red-600/20 text-red-400 border-red-500/30', label: `${pct}%`, quality: 'Low' }
}

type FormState = {
  name: string; mac: string; targetType: string; description: string
  latitude: string; longitude: string; url: string; tags: string; notes: string
}

const emptyForm: FormState = { name: '', mac: '', targetType: 'wifi', description: '', latitude: '', longitude: '', url: '', tags: '', notes: '' }

export default function TargetsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [search, setSearch] = useState('')

  const { data: targets, isLoading, error } = useQuery<Target[]>({
    queryKey: ['targets'],
    queryFn: () => api.get('/targets'),
    refetchInterval: 10000,
  })

  const createMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.post('/targets', body),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['targets'] }); setShowCreate(false); setEditingId(null); setForm(emptyForm) },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) => api.put(`/targets/${id}`, body),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['targets'] }); setEditingId(null); setForm(emptyForm) },
  })

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

  const startEdit = (t: Target) => {
    setEditingId(t.id)
    setShowCreate(true)
    setForm({
      name: t.name, mac: t.mac || '', targetType: t.targetType || 'wifi',
      description: t.description || '', latitude: t.latitude ? String(t.latitude) : '',
      longitude: t.longitude ? String(t.longitude) : '', url: t.url || '',
      tags: (t.tags || []).join(', '), notes: t.notes || '',
    })
  }

  const handleSave = () => {
    const body: Record<string, unknown> = {
      name: form.name, mac: form.mac || undefined, targetType: form.targetType,
      description: form.description || undefined, status: 'active',
      url: form.url || undefined, notes: form.notes || undefined,
      tags: form.tags ? form.tags.split(',').map(s => s.trim()).filter(Boolean) : [],
    }
    if (form.latitude) body.latitude = parseFloat(form.latitude)
    if (form.longitude) body.longitude = parseFloat(form.longitude)
    if (editingId) { updateMutation.mutate({ id: editingId, body }) } else { createMutation.mutate(body) }
  }

  const filtered = useMemo(() => {
    if (!targets) return []
    const q = search.toLowerCase().trim()
    let list = targets
    if (q) {
      list = list.filter(t =>
        t.name.toLowerCase().includes(q) ||
        (t.mac || '').toLowerCase().includes(q) ||
        (t.description || '').toLowerCase().includes(q) ||
        (t.notes || '').toLowerCase().includes(q) ||
        (t.tags || []).some(tag => tag.toLowerCase().includes(q))
      )
    }
    return [...list].sort((a, b) => {
      if (a.status !== b.status) return a.status === 'active' ? -1 : 1
      return new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
    })
  }, [targets, search])

  const f = (key: keyof FormState, val: string) => setForm(prev => ({ ...prev, [key]: val }))
  const inputCls = "px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Targets</h2>
          <p className="text-sm text-dark-400 mt-1">
            {filtered.filter(t => t.status === 'active').length} active / {(targets || []).length} total
          </p>
        </div>
        <div className="flex items-center gap-3">
          <input type="text" value={search} onChange={e => setSearch(e.target.value)}
            placeholder="Search name, MAC, tags..."
            className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-sm text-dark-200 placeholder-dark-500 focus:outline-none focus:border-primary-500 w-56" />
          <button onClick={() => { if (confirm('Clear all?')) clearMutation.mutate() }}
            className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors">Clear All</button>
          <button onClick={() => { setShowCreate(!showCreate); if (showCreate) { setEditingId(null); setForm(emptyForm) } }}
            className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors">
            {showCreate ? 'Cancel' : 'Add Target'}
          </button>
        </div>
      </div>

      {showCreate && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <h3 className="text-sm font-medium text-dark-200 mb-3">{editingId ? 'Edit Target' : 'Create Target'}</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            <input type="text" placeholder="Name *" value={form.name} onChange={e => f('name', e.target.value)} className={inputCls} />
            <input type="text" placeholder="MAC Address" value={form.mac} onChange={e => f('mac', e.target.value)} className={inputCls} />
            <select value={form.targetType} onChange={e => f('targetType', e.target.value)} className={inputCls}>
              <option value="wifi">WiFi</option>
              <option value="ble">BLE</option>
              <option value="drone">Drone</option>
              <option value="vehicle">Vehicle</option>
              <option value="person">Person</option>
            </select>
            <input type="text" placeholder="Description" value={form.description} onChange={e => f('description', e.target.value)} className={inputCls} />
            <input type="text" placeholder="URL" value={form.url} onChange={e => f('url', e.target.value)} className={inputCls} />
            <input type="text" placeholder="Tags (comma-separated)" value={form.tags} onChange={e => f('tags', e.target.value)} className={inputCls} />
            <input type="text" placeholder="Latitude" value={form.latitude} onChange={e => f('latitude', e.target.value)} className={inputCls} />
            <input type="text" placeholder="Longitude" value={form.longitude} onChange={e => f('longitude', e.target.value)} className={inputCls} />
            <textarea placeholder="Notes" value={form.notes} onChange={e => f('notes', e.target.value)} rows={2}
              className={`${inputCls} resize-none`} />
          </div>
          <div className="flex items-center gap-2 mt-3">
            <button onClick={handleSave} disabled={!form.name || createMutation.isPending || updateMutation.isPending}
              className="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors">
              {editingId ? 'Save' : 'Create'}
            </button>
          </div>
        </div>
      )}

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center"><div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" /><p className="mt-2 text-sm text-dark-400">Loading...</p></div>
        ) : error ? (
          <div className="p-8 text-center"><p className="text-sm text-red-400">Failed to load targets</p></div>
        ) : filtered.length === 0 ? (
          <div className="p-8 text-center"><p className="text-sm text-dark-400">{search ? 'No targets match' : 'No targets tracked'}</p></div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Name</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Type</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">MAC</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Status</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Confidence</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Position</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Tags</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {filtered.map((t) => {
                  const cb = confidenceBadge(t.trackingConfidence, t.trackingUncertainty)
                  return (
                    <tr key={t.id} className="hover:bg-dark-800/30 transition-colors">
                      <td className="px-4 py-3">
                        <div className="text-sm text-dark-200 font-medium">{t.name}</div>
                        {t.description && <div className="text-xs text-dark-500 mt-0.5 truncate max-w-[200px]">{t.description}</div>}
                        {t.notes && <div className="text-[10px] text-dark-600 mt-0.5 truncate max-w-[200px]">{t.notes}</div>}
                      </td>
                      <td className="px-4 py-3 text-sm text-dark-300">{t.targetType || '-'}</td>
                      <td className="px-4 py-3 text-sm text-dark-300 font-mono">{t.mac || '-'}</td>
                      <td className="px-4 py-3">
                        <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${statusBadge[t.status] || statusBadge.active}`}>
                          {t.status}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1.5">
                          <span className={`inline-flex px-1.5 py-0.5 text-[10px] font-medium rounded border ${cb.color}`}>
                            {cb.label}
                          </span>
                          {t.trackingUncertainty != null && t.trackingUncertainty > 0 && (
                            <span className="text-[10px] text-dark-500">&plusmn;{Math.round(t.trackingUncertainty)}m</span>
                          )}
                        </div>
                        {t.triangulationMethod && (
                          <div className="text-[9px] text-dark-600 mt-0.5">{t.triangulationMethod}</div>
                        )}
                      </td>
                      <td className="px-4 py-3 text-sm text-dark-400 font-mono">
                        {t.latitude && t.longitude ? `${t.latitude.toFixed(5)}, ${t.longitude.toFixed(5)}` : '-'}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex flex-wrap gap-1">
                          {(t.tags || []).map((tag, i) => (
                            <span key={i} className="text-[10px] px-1.5 py-0.5 rounded bg-primary-500/10 text-primary-400 border border-primary-500/20">{tag}</span>
                          ))}
                        </div>
                      </td>
                      <td className="px-4 py-3 text-right space-x-3">
                        <button onClick={() => startEdit(t)} className="text-xs text-primary-400 hover:text-primary-300 transition-colors">Edit</button>
                        {t.status === 'active' && (
                          <button onClick={() => resolveMutation.mutate(t.id)} className="text-xs text-yellow-400 hover:text-yellow-300 transition-colors">Resolve</button>
                        )}
                        <button onClick={() => { if (confirm(`Delete "${t.name}"?`)) deleteMutation.mutate(t.id) }}
                          className="text-xs text-red-400 hover:text-red-300 transition-colors">Delete</button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
