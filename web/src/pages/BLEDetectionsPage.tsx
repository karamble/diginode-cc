import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface BLEDetection {
  id: number
  mac: string
  node_id: string
  rssi: number
  channel: number
  timestamp: string
  detection_type?: string
  manufacturer?: string
  manufacturer_id?: number
  local_name?: string
  appearance?: number
  service_uuids_16?: number[]
  service_uuids_128?: string[]
  tx_power?: number
  is_random_addr: boolean
  raw_adv: string
  classification?: Record<string, unknown>
  findmy_score?: number
  combined_score?: number
}

// detectionBadge picks a colour for the detection_type chip. Trackers and
// surveillance hardware are red; sensors and appliances are amber; everything
// else is teal. Keeps the UI legible at a glance without a legend.
function detectionBadge(t?: string): { color: string; label: string } {
  if (!t) return { color: 'bg-dark-600 text-dark-300 border-dark-500/40', label: 'unknown' }
  const lower = t.toLowerCase()
  const tracker = ['airtag', 'tile', 'samsung_smarttag', 'chipolo', 'pebblebee', 'eufy', 'nut', 'macless_haystack']
  if (tracker.includes(lower)) {
    return { color: 'bg-red-500/20 text-red-300 border-red-500/30', label: lower }
  }
  if (lower.startsWith('surveillance')) {
    return { color: 'bg-red-500/20 text-red-300 border-red-500/30', label: lower.replace('surveillance_', '') }
  }
  if (lower.includes('sensor') || lower === 'domestic_appliance' || lower === 'cookware') {
    return { color: 'bg-amber-500/20 text-amber-300 border-amber-500/30', label: lower }
  }
  return { color: 'bg-teal-500/20 text-teal-300 border-teal-500/30', label: lower }
}

function rssiColor(rssi: number): string {
  if (rssi >= -60) return 'bg-emerald-500/30 text-emerald-300'
  if (rssi >= -75) return 'bg-amber-500/30 text-amber-300'
  return 'bg-red-500/30 text-red-300'
}

function timeAgo(iso: string): string {
  const t = new Date(iso).getTime()
  if (!isFinite(t)) return '-'
  const diff = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

export default function BLEDetectionsPage() {
  const [typeFilter, setTypeFilter] = useState<string>('')
  const [macFilter, setMacFilter] = useState<string>('')
  const [markRow, setMarkRow] = useState<BLEDetection | null>(null)

  const params = new URLSearchParams()
  if (typeFilter) params.set('type', typeFilter)
  if (macFilter) params.set('mac', macFilter)
  params.set('limit', '500')
  const qs = params.toString()

  const { data, isLoading, refetch } = useQuery<BLEDetection[]>({
    queryKey: ['ble-detections', typeFilter, macFilter],
    queryFn: () => api.get<BLEDetection[]>(`/ble/detections?${qs}`),
    refetchInterval: 5000,
  })

  const rows = data ?? []
  const trackerCount = rows.filter((r) => {
    const lower = r.detection_type?.toLowerCase() ?? ''
    return ['airtag', 'tile', 'samsung_smarttag', 'chipolo', 'pebblebee', 'eufy', 'nut', 'macless_haystack'].includes(lower)
  }).length
  const surveillanceCount = rows.filter((r) => r.detection_type?.toLowerCase().startsWith('surveillance_')).length

  return (
    <div className="p-4">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-dark-100">
            BLE Detections
            <span className="ml-2 inline-block px-1.5 py-0.5 text-xs rounded border border-violet-500/40 bg-violet-500/10 text-violet-300 font-mono align-middle">
              RAW classified
            </span>
          </h1>
          <p className="text-sm text-dark-400">
            {rows.length} rows ・ {trackerCount} trackers ・ {surveillanceCount} surveillance ・ each row sourced from a B:/BLERAW: raw advertisement frame and identified by the lookupper cascade
          </p>
        </div>
        <button
          onClick={() => refetch()}
          className="px-3 py-1.5 text-sm rounded bg-dark-700 hover:bg-dark-600 text-dark-100 border border-dark-600"
        >
          Refresh
        </button>
      </div>

      <div className="mb-4 flex gap-3">
        <input
          type="text"
          placeholder="Filter by detection type (e.g. airtag, tile)"
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value)}
          className="flex-1 px-3 py-1.5 text-sm rounded bg-dark-800 border border-dark-600 text-dark-100"
        />
        <input
          type="text"
          placeholder="Filter by MAC (AA:BB:CC:DD:EE:FF)"
          value={macFilter}
          onChange={(e) => setMacFilter(e.target.value.toUpperCase())}
          className="flex-1 px-3 py-1.5 text-sm rounded bg-dark-800 border border-dark-600 text-dark-100"
        />
      </div>

      {isLoading ? (
        <p className="text-dark-400">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="text-dark-400">
          No BLE detections yet. Enable raw mode on a sensor with{' '}
          <code className="bg-dark-700 px-1 rounded">@HBxx RAW_BLE_ON</code> from CommandsPage and run a device scan.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm border-collapse">
            <thead>
              <tr className="text-left text-dark-400 border-b border-dark-700">
                <th className="px-2 py-2">Type</th>
                <th className="px-2 py-2">MAC</th>
                <th className="px-2 py-2">Manufacturer</th>
                <th className="px-2 py-2">Local Name</th>
                <th className="px-2 py-2">RSSI</th>
                <th className="px-2 py-2">CH</th>
                <th className="px-2 py-2">FindMy</th>
                <th className="px-2 py-2">Node</th>
                <th className="px-2 py-2">Seen</th>
                <th className="px-2 py-2">Action</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => {
                const badge = detectionBadge(r.detection_type)
                return (
                  <tr key={r.id} className="border-b border-dark-800 hover:bg-dark-800/40">
                    <td className="px-2 py-1.5">
                      <span className={`inline-block px-2 py-0.5 text-xs rounded border ${badge.color}`}>{badge.label}</span>
                      <span
                        className="ml-1 inline-block px-1 py-0.5 text-[10px] rounded border border-violet-500/40 bg-violet-500/10 text-violet-300 font-mono align-middle"
                        title="Classified from a raw BLE advertisement frame"
                      >
                        RAW
                      </span>
                    </td>
                    <td className="px-2 py-1.5 font-mono text-dark-200">
                      {r.mac}
                      {r.is_random_addr && <span className="ml-1 text-xs text-dark-500">(rnd)</span>}
                    </td>
                    <td className="px-2 py-1.5 text-dark-300">{r.manufacturer || '-'}</td>
                    <td className="px-2 py-1.5 text-dark-300">{r.local_name || '-'}</td>
                    <td className="px-2 py-1.5">
                      <span className={`px-1.5 py-0.5 text-xs rounded ${rssiColor(r.rssi)}`}>{r.rssi} dBm</span>
                    </td>
                    <td className="px-2 py-1.5 text-dark-400">{r.channel || '-'}</td>
                    <td className="px-2 py-1.5 text-dark-400">{r.findmy_score ?? '-'}</td>
                    <td className="px-2 py-1.5 font-mono text-dark-300">{r.node_id}</td>
                    <td className="px-2 py-1.5 text-dark-400">{timeAgo(r.timestamp)}</td>
                    <td className="px-2 py-1.5">
                      <button
                        onClick={() => setMarkRow(r)}
                        className="text-[11px] px-2 py-0.5 rounded border border-violet-500/40 bg-violet-500/10 text-violet-300 hover:bg-violet-500/20 transition-colors"
                        title="Create a BLE fingerprint target from this row"
                      >
                        Mark as target
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {markRow && (
        <MarkAsBLETargetDialog
          row={markRow}
          onClose={() => setMarkRow(null)}
        />
      )}
    </div>
  )
}

// MarkAsBLETargetDialog opens when the operator clicks "Mark as target" on
// a BLE Detections row. The form pre-fills the fingerprint fields from the
// row's classification result; checkboxes control which fields go into the
// fingerprint pattern. Submit POSTs to /targets/ble which allocates a fresh
// T-B-#### identifier and persists the target. The operator then has to
// push the targets to a Halberd node manually via CommandsPage's
// CONFIG_TARGETS_BLE command (target sync stays operator-driven, same as
// the existing WiFi CONFIG_TARGETS workflow).
function MarkAsBLETargetDialog({ row, onClose }: { row: BLEDetection; onClose: () => void }) {
  const queryClient = useQueryClient()
  const defaultName = row.local_name && row.local_name.trim().length > 0
    ? row.local_name
    : (row.manufacturer || 'BLE target')

  const [name, setName] = useState(defaultName)
  const [description, setDescription] = useState('')
  const [pickMfr, setPickMfr] = useState(row.manufacturer_id != null)
  const [pickUuid16, setPickUuid16] = useState<Set<number>>(new Set(row.service_uuids_16 ?? []))
  const [pickUuid128, setPickUuid128] = useState<Set<string>>(new Set(row.service_uuids_128 ?? []))
  const [pickName, setPickName] = useState(!!row.local_name)
  const [namePattern, setNamePattern] = useState(row.local_name ?? '')
  const [pickAppearance, setPickAppearance] = useState(row.appearance != null)
  const [appMin, setAppMin] = useState<string>(row.appearance != null ? String(row.appearance) : '')
  const [appMax, setAppMax] = useState<string>(row.appearance != null ? String(row.appearance) : '')
  const [pickTxPower, setPickTxPower] = useState(false)
  const [txMin, setTxMin] = useState<string>(row.tx_power != null ? String(row.tx_power) : '')
  const [txMax, setTxMax] = useState<string>(row.tx_power != null ? String(row.tx_power) : '')
  const [matchMode, setMatchMode] = useState<'ALL' | 'ANY'>('ALL')
  const [error, setError] = useState<string>('')

  const create = useMutation({
    mutationFn: async () => {
      const fingerprint: Record<string, unknown> = { matchMode }
      if (pickMfr && row.manufacturer_id != null) fingerprint.manufacturerId = row.manufacturer_id
      if (pickUuid16.size > 0) fingerprint.serviceUuids16 = Array.from(pickUuid16)
      if (pickUuid128.size > 0) fingerprint.serviceUuids128 = Array.from(pickUuid128)
      if (pickName && namePattern.trim().length > 0) fingerprint.localNameGlob = namePattern.trim()
      if (pickAppearance) {
        if (appMin.trim() !== '') fingerprint.appearanceMin = parseInt(appMin, 10)
        if (appMax.trim() !== '') fingerprint.appearanceMax = parseInt(appMax, 10)
      }
      if (pickTxPower) {
        if (txMin.trim() !== '') fingerprint.txPowerMin = parseInt(txMin, 10)
        if (txMax.trim() !== '') fingerprint.txPowerMax = parseInt(txMax, 10)
      }
      return api.post('/targets/ble', {
        name,
        description,
        detectionId: row.id,
        fingerprint,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['targets'] })
      onClose()
    },
    onError: (e: unknown) => {
      setError(e instanceof Error ? e.message : String(e))
    },
  })

  const noFieldChosen = !pickMfr && pickUuid16.size === 0 && pickUuid128.size === 0 &&
                       !pickName && !pickAppearance && !pickTxPower

  const toggleUuid16 = (u: number) => {
    const next = new Set(pickUuid16)
    if (next.has(u)) next.delete(u)
    else next.add(u)
    setPickUuid16(next)
  }
  const toggleUuid128 = (u: string) => {
    const next = new Set(pickUuid128)
    if (next.has(u)) next.delete(u)
    else next.add(u)
    setPickUuid128(next)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className="bg-dark-900 border border-dark-700 rounded-lg shadow-2xl max-w-xl w-full max-h-[90vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="p-4 border-b border-dark-700 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-dark-100">Mark as BLE Fingerprint Target</h2>
          <button onClick={onClose} className="text-dark-400 hover:text-dark-100 text-lg">&times;</button>
        </div>

        <div className="p-4 space-y-3 text-sm">
          <div>
            <label className="block text-[11px] text-dark-400 mb-1">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-2 py-1 bg-dark-800 border border-dark-600 rounded text-dark-100 text-xs"
            />
          </div>

          <div>
            <label className="block text-[11px] text-dark-400 mb-1">Description (optional)</label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={2}
              className="w-full px-2 py-1 bg-dark-800 border border-dark-600 rounded text-dark-100 text-xs"
            />
          </div>

          <div className="border-t border-dark-700 pt-3">
            <div className="text-[11px] text-dark-400 mb-2">Fingerprint fields (pick which to match on)</div>

            {row.manufacturer_id != null && (
              <label className="flex items-center gap-2 mb-2">
                <input type="checkbox" checked={pickMfr} onChange={(e) => setPickMfr(e.target.checked)} />
                <span className="text-xs text-dark-200">
                  Manufacturer ID: <span className="font-mono">0x{row.manufacturer_id.toString(16).padStart(4, '0')}</span>
                  {row.manufacturer && <span className="text-dark-400"> ({row.manufacturer})</span>}
                </span>
              </label>
            )}

            {(row.service_uuids_16 ?? []).length > 0 && (
              <div className="mb-2">
                <div className="text-xs text-dark-300 mb-1">16-bit service UUIDs (OR / any-of):</div>
                {(row.service_uuids_16 ?? []).map((u) => (
                  <label key={u} className="flex items-center gap-2 ml-2 text-xs">
                    <input type="checkbox" checked={pickUuid16.has(u)} onChange={() => toggleUuid16(u)} />
                    <span className="font-mono text-dark-200">0x{u.toString(16).padStart(4, '0').toUpperCase()}</span>
                  </label>
                ))}
              </div>
            )}

            {(row.service_uuids_128 ?? []).length > 0 && (
              <div className="mb-2">
                <div className="text-xs text-dark-300 mb-1">128-bit service UUIDs (OR / any-of):</div>
                {(row.service_uuids_128 ?? []).map((u) => (
                  <label key={u} className="flex items-center gap-2 ml-2 text-xs">
                    <input type="checkbox" checked={pickUuid128.has(u)} onChange={() => toggleUuid128(u)} />
                    <span className="font-mono text-dark-200 break-all">{u}</span>
                  </label>
                ))}
              </div>
            )}

            <label className="flex items-center gap-2 mb-1">
              <input type="checkbox" checked={pickName} onChange={(e) => setPickName(e.target.checked)} />
              <span className="text-xs text-dark-200">Local name glob (use trailing <span className="font-mono">*</span> for prefix match)</span>
            </label>
            {pickName && (
              <input
                type="text"
                value={namePattern}
                onChange={(e) => setNamePattern(e.target.value)}
                placeholder="Forerunner *"
                className="w-full px-2 py-1 bg-dark-800 border border-dark-600 rounded text-dark-100 text-xs ml-6"
              />
            )}

            {row.appearance != null && (
              <div className="mt-2">
                <label className="flex items-center gap-2 mb-1">
                  <input type="checkbox" checked={pickAppearance} onChange={(e) => setPickAppearance(e.target.checked)} />
                  <span className="text-xs text-dark-200">Appearance (SIG category): observed <span className="font-mono">{row.appearance}</span></span>
                </label>
                {pickAppearance && (
                  <div className="flex gap-2 ml-6 text-xs">
                    <span className="text-dark-400">min</span>
                    <input type="number" value={appMin} onChange={(e) => setAppMin(e.target.value)}
                           className="w-20 px-1 py-0.5 bg-dark-800 border border-dark-600 rounded" />
                    <span className="text-dark-400">max</span>
                    <input type="number" value={appMax} onChange={(e) => setAppMax(e.target.value)}
                           className="w-20 px-1 py-0.5 bg-dark-800 border border-dark-600 rounded" />
                  </div>
                )}
              </div>
            )}

            {row.tx_power != null && (
              <div className="mt-2">
                <label className="flex items-center gap-2 mb-1">
                  <input type="checkbox" checked={pickTxPower} onChange={(e) => setPickTxPower(e.target.checked)} />
                  <span className="text-xs text-dark-200">TX power (dBm): observed <span className="font-mono">{row.tx_power}</span></span>
                </label>
                {pickTxPower && (
                  <div className="flex gap-2 ml-6 text-xs">
                    <span className="text-dark-400">min</span>
                    <input type="number" value={txMin} onChange={(e) => setTxMin(e.target.value)}
                           className="w-20 px-1 py-0.5 bg-dark-800 border border-dark-600 rounded" />
                    <span className="text-dark-400">max</span>
                    <input type="number" value={txMax} onChange={(e) => setTxMax(e.target.value)}
                           className="w-20 px-1 py-0.5 bg-dark-800 border border-dark-600 rounded" />
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="border-t border-dark-700 pt-3">
            <div className="text-[11px] text-dark-400 mb-1">Match mode</div>
            <div className="flex gap-2">
              <button
                onClick={() => setMatchMode('ALL')}
                className={`px-3 py-1 text-xs rounded border ${matchMode === 'ALL' ? 'bg-violet-500/20 text-violet-300 border-violet-500/40' : 'bg-dark-800 text-dark-300 border-dark-600'}`}
              >
                ALL (AND across picked fields)
              </button>
              <button
                onClick={() => setMatchMode('ANY')}
                className={`px-3 py-1 text-xs rounded border ${matchMode === 'ANY' ? 'bg-violet-500/20 text-violet-300 border-violet-500/40' : 'bg-dark-800 text-dark-300 border-dark-600'}`}
              >
                ANY (any one matches)
              </button>
            </div>
          </div>

          {error && (
            <div className="text-xs text-red-300 bg-red-500/10 border border-red-500/30 rounded p-2">{error}</div>
          )}

          <div className="text-[10px] text-dark-500">
            After saving, push the target to a Halberd node from the Commands page using <span className="font-mono">CONFIG_TARGETS_BLE</span>. Hits arrive as <span className="font-mono text-violet-300">ble-target-hit</span> alerts.
          </div>
        </div>

        <div className="p-4 border-t border-dark-700 flex justify-end gap-2">
          <button onClick={onClose} className="px-3 py-1.5 text-xs rounded bg-dark-700 hover:bg-dark-600 text-dark-200">
            Cancel
          </button>
          <button
            onClick={() => create.mutate()}
            disabled={noFieldChosen || create.isPending}
            className="px-3 py-1.5 text-xs rounded bg-violet-600 hover:bg-violet-500 text-white disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {create.isPending ? 'Creating…' : 'Create target'}
          </button>
        </div>
      </div>
    </div>
  )
}
