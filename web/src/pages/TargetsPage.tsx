import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo } from 'react'
import api from '../api/client'
import TargetHitsModal from '../components/TargetHitsModal'

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
  // BLE fingerprint fields — non-empty bleShortId discriminates BLE rows
  // from WiFi/MAC/SSID rows. The targetType column also says
  // "BLE_FINGERPRINT" for these but the bleShortId presence is the
  // canonical check used in render code.
  bleShortId?: string
  bleManufacturerId?: number
  bleServiceUuids16?: number[]
  bleServiceUuids128?: string[]
  bleLocalNameGlob?: string
  bleAppearanceMin?: number
  bleAppearanceMax?: number
  bleTxPowerMin?: number
  bleTxPowerMax?: number
  bleMatchMode?: string
  // Server-side fallback position pulled from the latest target_hits row
  // when triangulation hasn't produced a fix yet. Used for the "~lat,lon"
  // approximate display in the Position cell.
  lastHitLatitude?: number
  lastHitLongitude?: number
}

// fingerprintSummary renders the BLE fingerprint fields into a short
// "mfr=Garmin uuid=FEF8 name=Forerunner *" preview suitable for the
// table's MAC/Identity column.
function fingerprintSummary(t: Target): string {
  const parts: string[] = []
  if (t.bleManufacturerId != null) parts.push(`mfr=0x${t.bleManufacturerId.toString(16).padStart(4, '0')}`)
  if (t.bleServiceUuids16 && t.bleServiceUuids16.length > 0) {
    parts.push(`uuid=${t.bleServiceUuids16.map(u => u.toString(16).padStart(4, '0').toUpperCase()).join(',')}`)
  }
  if (t.bleServiceUuids128 && t.bleServiceUuids128.length > 0) parts.push(`uuid128=${t.bleServiceUuids128.length}`)
  if (t.bleLocalNameGlob) parts.push(`name="${t.bleLocalNameGlob}"`)
  if (t.bleAppearanceMin != null || t.bleAppearanceMax != null) {
    parts.push(`app=${t.bleAppearanceMin ?? '*'}..${t.bleAppearanceMax ?? '*'}`)
  }
  if (t.bleTxPowerMin != null || t.bleTxPowerMax != null) {
    parts.push(`tx=${t.bleTxPowerMin ?? '*'}..${t.bleTxPowerMax ?? '*'}`)
  }
  if (t.bleMatchMode && t.bleMatchMode !== 'ALL') parts.push(`match=${t.bleMatchMode}`)
  return parts.join(' ')
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
  // BLE fingerprint fields, only meaningful when editing a BLE target row.
  // Stored as raw strings so the operator can use the same hex shorthand
  // ("0087" / "FEF8,180D") as the Mark-as-target dialog on BLEDetectionsPage.
  bleEditing: boolean        // true when the form is editing an existing BLE row
  bleShortId: string         // read-only, displayed in the form for reference
  bleManufacturerHex: string // 4-char hex without 0x prefix; "" = wildcard
  bleUuid16Csv: string       // comma-separated 4-char hex (e.g. "FEF8,180D"); "" = wildcard
  bleUuid128Csv: string      // comma-separated full UUID strings; "" = wildcard
  bleNameGlob: string        // optional trailing-* glob; "" = wildcard
  bleAppearanceMin: string   // signed/unsigned int as string; "" = wildcard
  bleAppearanceMax: string
  bleTxPowerMin: string
  bleTxPowerMax: string
  bleMatchMode: 'ALL' | 'ANY'
}

const emptyForm: FormState = {
  name: '', mac: '', targetType: 'wifi', description: '', latitude: '', longitude: '', url: '', tags: '', notes: '',
  bleEditing: false, bleShortId: '', bleManufacturerHex: '', bleUuid16Csv: '', bleUuid128Csv: '',
  bleNameGlob: '', bleAppearanceMin: '', bleAppearanceMax: '', bleTxPowerMin: '', bleTxPowerMax: '',
  bleMatchMode: 'ALL',
}

export default function TargetsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [search, setSearch] = useState('')
  const [historyTarget, setHistoryTarget] = useState<Target | null>(null)
  const [syncNodeTarget, setSyncNodeTarget] = useState<string>('')
  const [syncFlash, setSyncFlash] = useState<{ kind: 'push' | 'clear' | 'error'; text: string } | null>(null)

  // AntiHunter mesh nodes (Halberd sensors). Filter to nodes that
  // accept CONFIG_TARGETS_BLE — i.e. nodeType === 'antihunter' with a
  // sensorShortId (HB##) — the same target shape the CommandsPage uses.
  interface SyncNode { id: string; nodeNum: number; sensorShortId?: string; nodeType?: string; isOnline: boolean }
  const { data: nodesData } = useQuery<SyncNode[]>({
    queryKey: ['nodes'],
    queryFn: () => api.get('/nodes'),
    refetchInterval: 30000,
  })
  const ahNodes = useMemo(
    () => (nodesData || []).filter(n => n.nodeType === 'antihunter' && !!n.sensorShortId),
    [nodesData],
  )
  const syncTargetWire = useMemo(() => {
    if (!syncNodeTarget) return ''
    if (syncNodeTarget === '@ALL') return '@ALL'
    return '@' + syncNodeTarget
  }, [syncNodeTarget])

  const pushBLEMutation = useMutation({
    mutationFn: async () => {
      const bleTargets = (targets || []).filter(t => !!t.bleShortId)
      if (bleTargets.length === 0) throw new Error('No BLE targets to push')
      if (!syncTargetWire) throw new Error('Pick a target node first')
      return api.post('/commands', {
        target: syncTargetWire,
        name: 'CONFIG_TARGETS_BLE',
        params: [bleTargets.map(t => t.bleShortId).join(',')],
        forever: false,
      })
    },
    onSuccess: () => {
      setSyncFlash({ kind: 'push', text: `Push queued to ${syncTargetWire}` })
      setTimeout(() => setSyncFlash(null), 4000)
    },
    onError: (e: Error) => {
      setSyncFlash({ kind: 'error', text: e.message })
      setTimeout(() => setSyncFlash(null), 4000)
    },
  })

  const clearBLEMutation = useMutation({
    mutationFn: async () => {
      if (!syncTargetWire) throw new Error('Pick a target node first')
      return api.post('/commands', {
        target: syncTargetWire,
        name: 'CONFIG_TARGETS_BLE',
        params: [],
        forever: false,
      })
    },
    onSuccess: () => {
      setSyncFlash({ kind: 'clear', text: `Clear queued to ${syncTargetWire}` })
      setTimeout(() => setSyncFlash(null), 4000)
    },
    onError: (e: Error) => {
      setSyncFlash({ kind: 'error', text: e.message })
      setTimeout(() => setSyncFlash(null), 4000)
    },
  })

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

  const reactivateMutation = useMutation({
    mutationFn: (id: string) => api.post(`/targets/${id}/reactivate`),
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
    const isBLE = !!t.bleShortId
    setForm({
      name: t.name, mac: t.mac || '', targetType: t.targetType || 'wifi',
      description: t.description || '', latitude: t.latitude ? String(t.latitude) : '',
      longitude: t.longitude ? String(t.longitude) : '', url: t.url || '',
      tags: (t.tags || []).join(', '), notes: t.notes || '',
      bleEditing: isBLE,
      bleShortId: t.bleShortId || '',
      bleManufacturerHex: t.bleManufacturerId != null ? t.bleManufacturerId.toString(16).padStart(4, '0') : '',
      bleUuid16Csv: (t.bleServiceUuids16 || []).map(u => u.toString(16).padStart(4, '0').toUpperCase()).join(','),
      bleUuid128Csv: (t.bleServiceUuids128 || []).join(','),
      bleNameGlob: t.bleLocalNameGlob || '',
      bleAppearanceMin: t.bleAppearanceMin != null ? String(t.bleAppearanceMin) : '',
      bleAppearanceMax: t.bleAppearanceMax != null ? String(t.bleAppearanceMax) : '',
      bleTxPowerMin: t.bleTxPowerMin != null ? String(t.bleTxPowerMin) : '',
      bleTxPowerMax: t.bleTxPowerMax != null ? String(t.bleTxPowerMax) : '',
      bleMatchMode: (t.bleMatchMode === 'ANY' ? 'ANY' : 'ALL'),
    })
  }

  // parseHex16 turns "0087", "0x0087", or "87" into 135. Returns null on
  // invalid input so the form can either skip the field (empty) or surface
  // a save error rather than silently saving a wildcard.
  const parseHex16 = (s: string): number | null => {
    const t = s.trim().replace(/^0x/i, '')
    if (!t) return null
    if (!/^[0-9a-f]{1,4}$/i.test(t)) return null
    return parseInt(t, 16)
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

    if (form.bleEditing && editingId) {
      // Pack BLE fingerprint fields into the body using the Target row's
      // json shape (bleManufacturerId, bleServiceUuids16, ...). Empty string
      // means wildcard for that field — server stores NULL.
      const mfr = parseHex16(form.bleManufacturerHex)
      if (mfr != null) body.bleManufacturerId = mfr
      else if (form.bleManufacturerHex.trim() === '') body.bleManufacturerId = null

      const uuid16: number[] = []
      for (const part of form.bleUuid16Csv.split(',')) {
        const trimmed = part.trim()
        if (!trimmed) continue
        const v = parseHex16(trimmed)
        if (v != null) uuid16.push(v)
      }
      body.bleServiceUuids16 = uuid16

      const uuid128 = form.bleUuid128Csv.split(',').map(s => s.trim()).filter(Boolean)
      body.bleServiceUuids128 = uuid128

      body.bleLocalNameGlob = form.bleNameGlob.trim() || null
      body.bleAppearanceMin = form.bleAppearanceMin.trim() === '' ? null : parseInt(form.bleAppearanceMin, 10)
      body.bleAppearanceMax = form.bleAppearanceMax.trim() === '' ? null : parseInt(form.bleAppearanceMax, 10)
      body.bleTxPowerMin = form.bleTxPowerMin.trim() === '' ? null : parseInt(form.bleTxPowerMin, 10)
      body.bleTxPowerMax = form.bleTxPowerMax.trim() === '' ? null : parseInt(form.bleTxPowerMax, 10)
      body.bleMatchMode = form.bleMatchMode
    }

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
      <div className="flex items-center justify-between mb-3">
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

      {/* Firmware sync row — push the C2's BLE target list to a Halberd
          sensor's NVS, or clear the sensor's list entirely. CONFIG_TARGETS_BLE
          is a full replace, so a "push" overwrites whatever was in NVS and
          a "clear" wipes the in-flash list. The CommandsPage flow stays
          available for advanced cases (subset push, replay, etc.). */}
      <div className="bg-surface rounded-lg border border-dark-700/50 px-4 py-2 mb-6 flex items-center gap-3 flex-wrap">
        <span className="text-xs text-dark-400 font-medium">Firmware sync</span>
        <select
          value={syncNodeTarget}
          onChange={e => setSyncNodeTarget(e.target.value)}
          className="px-2 py-1.5 bg-dark-800 border border-dark-600 rounded text-xs text-dark-200 font-mono focus:border-primary-500 focus:outline-none"
        >
          <option value="">— pick node —</option>
          <option value="@ALL">@ALL (all online sensors)</option>
          {ahNodes.map(n => (
            <option key={n.id} value={n.sensorShortId}>
              @{n.sensorShortId} {n.isOnline ? '· online' : '· offline'}
            </option>
          ))}
        </select>
        <button
          disabled={!syncNodeTarget || pushBLEMutation.isPending}
          onClick={() => pushBLEMutation.mutate()}
          className="px-3 py-1.5 bg-violet-600 hover:bg-violet-500 disabled:opacity-40 disabled:cursor-not-allowed text-white text-xs rounded font-medium transition-colors"
          title="Send CONFIG_TARGETS_BLE with every active BLE target in this list"
        >
          Push BLE targets
        </button>
        <button
          disabled={!syncNodeTarget || clearBLEMutation.isPending}
          onClick={() => { if (confirm(`Clear ${syncTargetWire || 'node'} BLE target list?`)) clearBLEMutation.mutate() }}
          className="px-3 py-1.5 bg-dark-700 hover:bg-dark-600 disabled:opacity-40 disabled:cursor-not-allowed text-dark-300 text-xs rounded font-medium transition-colors"
          title="Send CONFIG_TARGETS_BLE with empty body — wipes the sensor's blelist NVS key"
        >
          Clear firmware list
        </button>
        <span className="text-[10px] text-dark-500 ml-auto">
          {(targets || []).filter(t => !!t.bleShortId).length} BLE targets · CONFIG_TARGETS_BLE replaces in full
        </span>
        {syncFlash && (
          <span className={
            'text-[11px] font-mono px-2 py-0.5 rounded border ' +
            (syncFlash.kind === 'error'
              ? 'border-red-500/50 bg-red-500/10 text-red-300'
              : syncFlash.kind === 'clear'
                ? 'border-yellow-500/50 bg-yellow-500/10 text-yellow-300'
                : 'border-violet-500/50 bg-violet-500/10 text-violet-300')
          }>
            {syncFlash.text}
          </span>
        )}
      </div>

      {showCreate && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-medium text-dark-200">{editingId ? 'Edit Target' : 'Create Target'}</h3>
            {form.bleEditing && (
              <span className="inline-block px-2 py-0.5 text-[10px] rounded border border-violet-500/40 bg-violet-500/10 text-violet-300 font-mono">
                BLE FINGERPRINT {form.bleShortId}
              </span>
            )}
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            <input type="text" placeholder="Name *" value={form.name} onChange={e => f('name', e.target.value)} className={inputCls} />
            {!form.bleEditing && (
              <input type="text" placeholder="MAC Address" value={form.mac} onChange={e => f('mac', e.target.value)} className={inputCls} />
            )}
            <select value={form.targetType} onChange={e => f('targetType', e.target.value)} className={inputCls} disabled={form.bleEditing}>
              <option value="wifi">WiFi</option>
              <option value="ble">BLE</option>
              <option value="BLE_FINGERPRINT">BLE Fingerprint</option>
              <option value="drone">Drone</option>
              <option value="vehicle">Vehicle</option>
              <option value="person">Person</option>
            </select>
            <input type="text" placeholder="Description" value={form.description} onChange={e => f('description', e.target.value)} className={inputCls} />
            <input type="text" placeholder="URL" value={form.url} onChange={e => f('url', e.target.value)} className={inputCls} />
            <input type="text" placeholder="Tags (comma-separated)" value={form.tags} onChange={e => f('tags', e.target.value)} className={inputCls} />
            {!form.bleEditing && (
              <>
                <input type="text" placeholder="Latitude" value={form.latitude} onChange={e => f('latitude', e.target.value)} className={inputCls} />
                <input type="text" placeholder="Longitude" value={form.longitude} onChange={e => f('longitude', e.target.value)} className={inputCls} />
              </>
            )}
            <textarea placeholder="Notes" value={form.notes} onChange={e => f('notes', e.target.value)} rows={2}
              className={`${inputCls} resize-none`} />
          </div>

          {form.bleEditing && (
            <div className="mt-4 pt-4 border-t border-dark-700/50">
              <div className="flex items-center justify-between mb-2">
                <h4 className="text-xs font-medium text-violet-300 uppercase tracking-wider">BLE Fingerprint</h4>
                <span className="text-[10px] text-dark-500">leave a field blank to wildcard it · trailing <code className="text-dark-400">*</code> in name = prefix glob</span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                <div>
                  <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Manufacturer ID (hex)</label>
                  <input type="text" placeholder="e.g. 0087 (Garmin)" value={form.bleManufacturerHex}
                    onChange={e => f('bleManufacturerHex', e.target.value)} className={inputCls} />
                </div>
                <div className="lg:col-span-2">
                  <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Service UUIDs (16-bit, comma-separated hex)</label>
                  <input type="text" placeholder="e.g. FEF8,180D" value={form.bleUuid16Csv}
                    onChange={e => f('bleUuid16Csv', e.target.value)} className={inputCls + ' w-full'} />
                </div>
                <div className="lg:col-span-3">
                  <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Service UUIDs (128-bit, comma-separated)</label>
                  <input type="text" placeholder="e.g. 12345678-1234-5678-1234-567812345678" value={form.bleUuid128Csv}
                    onChange={e => f('bleUuid128Csv', e.target.value)} className={inputCls + ' w-full'} />
                </div>
                <div className="lg:col-span-2">
                  <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Local Name (trailing <code>*</code> = glob)</label>
                  <input type="text" placeholder="e.g. Forerunner *" value={form.bleNameGlob}
                    onChange={e => f('bleNameGlob', e.target.value)} className={inputCls + ' w-full'} />
                </div>
                <div>
                  <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Match Mode</label>
                  <select value={form.bleMatchMode}
                    onChange={e => setForm(prev => ({ ...prev, bleMatchMode: e.target.value === 'ANY' ? 'ANY' : 'ALL' }))}
                    className={inputCls + ' w-full'}>
                    <option value="ALL">ALL (AND across set fields)</option>
                    <option value="ANY">ANY (OR across set fields)</option>
                  </select>
                </div>
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Appearance Min</label>
                    <input type="text" placeholder="e.g. 192" value={form.bleAppearanceMin}
                      onChange={e => f('bleAppearanceMin', e.target.value)} className={inputCls + ' w-full'} />
                  </div>
                  <div>
                    <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">Appearance Max</label>
                    <input type="text" placeholder="e.g. 192" value={form.bleAppearanceMax}
                      onChange={e => f('bleAppearanceMax', e.target.value)} className={inputCls + ' w-full'} />
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">TX Power Min (dBm)</label>
                    <input type="text" placeholder="e.g. -90" value={form.bleTxPowerMin}
                      onChange={e => f('bleTxPowerMin', e.target.value)} className={inputCls + ' w-full'} />
                  </div>
                  <div>
                    <label className="block text-[10px] uppercase tracking-wider text-dark-500 mb-1">TX Power Max (dBm)</label>
                    <input type="text" placeholder="e.g. 20" value={form.bleTxPowerMax}
                      onChange={e => f('bleTxPowerMax', e.target.value)} className={inputCls + ' w-full'} />
                  </div>
                </div>
              </div>
              <p className="text-[10px] text-dark-500 mt-3">
                Changes here only affect what the C2 has on file. Push <code className="text-violet-300">CONFIG_TARGETS_BLE</code>
                {' '}from the Commands page to sync the firmware.
              </p>
            </div>
          )}

          {!editingId && (
            <p className="text-[10px] text-dark-500 mt-3">
              To create a BLE fingerprint target, open BLE Detections, find a row, and click "Mark as target".
              That dialog pre-fills the fingerprint from observed AD-payload fields.
            </p>
          )}

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
                        <div className="flex items-center gap-2">
                          <div className="text-sm text-dark-200 font-medium">{t.name}</div>
                          {t.bleShortId && (
                            <span
                              className="inline-block px-1.5 py-0.5 text-[10px] rounded border border-violet-500/40 bg-violet-500/10 text-violet-300 font-mono"
                              title={`BLE fingerprint target ${t.bleShortId}`}
                            >
                              BLE {t.bleShortId}
                            </span>
                          )}
                        </div>
                        {t.description && <div className="text-xs text-dark-500 mt-0.5 truncate max-w-[200px]">{t.description}</div>}
                        {t.notes && <div className="text-[10px] text-dark-600 mt-0.5 truncate max-w-[200px]">{t.notes}</div>}
                      </td>
                      <td className="px-4 py-3 text-sm text-dark-300">{t.targetType || '-'}</td>
                      <td className="px-4 py-3 text-sm text-dark-300 font-mono">
                        {t.bleShortId ? (
                          <span className="text-[11px] text-dark-400" title="BLE targets match by AD-payload fingerprint, not MAC">{fingerprintSummary(t) || '(empty)'}</span>
                        ) : (
                          t.mac || '-'
                        )}
                      </td>
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
                        {t.latitude && t.longitude
                          ? `${t.latitude.toFixed(5)}, ${t.longitude.toFixed(5)}`
                          : t.lastHitLatitude && t.lastHitLongitude
                            ? <span title="approximate — last hit GPS, no triangulation fix yet" className="text-yellow-400/70">{`~${t.lastHitLatitude.toFixed(5)}, ${t.lastHitLongitude.toFixed(5)}`}</span>
                            : '-'}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex flex-wrap gap-1">
                          {(t.tags || []).map((tag, i) => (
                            <span key={i} className="text-[10px] px-1.5 py-0.5 rounded bg-primary-500/10 text-primary-400 border border-primary-500/20">{tag}</span>
                          ))}
                        </div>
                      </td>
                      <td className="px-4 py-3 text-right space-x-3">
                        <button onClick={() => setHistoryTarget(t)} className="text-xs text-violet-400 hover:text-violet-300 transition-colors">History</button>
                        <button onClick={() => startEdit(t)} className="text-xs text-primary-400 hover:text-primary-300 transition-colors">Edit</button>
                        {t.status === 'active' && (
                          <button onClick={() => resolveMutation.mutate(t.id)} className="text-xs text-yellow-400 hover:text-yellow-300 transition-colors">Resolve</button>
                        )}
                        {t.status === 'resolved' && (
                          <button onClick={() => reactivateMutation.mutate(t.id)} className="text-xs text-green-400 hover:text-green-300 transition-colors">Reactivate</button>
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

      {historyTarget && (
        <TargetHitsModal
          targetId={historyTarget.id}
          targetName={historyTarget.name}
          targetShortId={historyTarget.bleShortId}
          onClose={() => setHistoryTarget(null)}
        />
      )}
    </div>
  )
}
