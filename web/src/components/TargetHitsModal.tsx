import { useEffect, useMemo, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { MapContainer, Marker, Popup, useMap } from 'react-leaflet'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import api from '../api/client'
import TileLayerControl from './TileLayerControl'

// TargetHit mirrors targets.Hit on the Go side. Server returns the rows
// newest-first via GET /targets/{id}/hits?limit=N.
interface TargetHit {
  id: string
  targetId: string
  targetShortId?: string
  observedMac: string
  observedName?: string
  rssi?: number
  latitude?: number
  longitude?: number
  nodeId?: string
  rawFrame?: string
  createdAt: string
}

interface Props {
  targetId: string
  targetName: string
  targetShortId?: string
  onClose: () => void
}

// Inject the dark-theme popup CSS once. Mirrors the pattern in DronesPage
// so the popup chrome looks the same across the app without leaking the
// override outside the modal lifetime.
function ensureDarkPopupStyle() {
  if (document.getElementById('target-hits-popup-style')) return
  const style = document.createElement('style')
  style.id = 'target-hits-popup-style'
  style.innerHTML = `
    .leaflet-popup-content-wrapper { background:#1e293b; color:#e2e8f0; border-radius:6px; }
    .leaflet-popup-tip { background:#1e293b; }
    .leaflet-popup-content { margin:8px 10px; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:11px; }
  `
  document.head.appendChild(style)
}

function FitBounds({ hits }: { hits: TargetHit[] }) {
  const map = useMap()
  useEffect(() => {
    const points = hits
      .filter((h) => h.latitude != null && h.longitude != null)
      .map((h) => [h.latitude as number, h.longitude as number] as [number, number])
    if (points.length === 0) return
    if (points.length === 1) {
      map.setView(points[0], 16)
      return
    }
    map.fitBounds(L.latLngBounds(points), { padding: [40, 40] })
  }, [hits, map])
  return null
}

// Two distinct icons so the most recent hit pops visually. Default colour
// for older hits, accent colour for the newest.
const latestIcon = L.divIcon({
  className: '',
  iconSize: [14, 14],
  iconAnchor: [7, 7],
  html: '<div style="width:14px;height:14px;border-radius:50%;background:#a78bfa;border:2px solid #f5f3ff;box-shadow:0 0 0 2px #6d28d9;"></div>',
})
const olderIcon = L.divIcon({
  className: '',
  iconSize: [10, 10],
  iconAnchor: [5, 5],
  html: '<div style="width:10px;height:10px;border-radius:50%;background:#64748b;border:1px solid #cbd5e1;"></div>',
})

export default function TargetHitsModal({ targetId, targetName, targetShortId, onClose }: Props) {
  const cardRef = useRef<HTMLDivElement>(null)
  const { data, isLoading, error } = useQuery<TargetHit[]>({
    queryKey: ['target-hits', targetId],
    queryFn: () => api.get(`/targets/${targetId}/hits?limit=500`),
    staleTime: 0,
    refetchInterval: 5000,
  })

  useEffect(() => {
    ensureDarkPopupStyle()
  }, [])

  const hits = useMemo(() => data ?? [], [data])
  const geocoded = useMemo(() => hits.filter((h) => h.latitude != null && h.longitude != null), [hits])
  const initialCenter: [number, number] = geocoded.length > 0
    ? [geocoded[0].latitude as number, geocoded[0].longitude as number]
    : [47.3769, 8.5417]

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={onClose}
    >
      <div
        ref={cardRef}
        className="bg-surface rounded-lg border border-dark-700/50 shadow-2xl w-[95vw] max-w-6xl h-[85vh] flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-4 py-3 border-b border-dark-700/50">
          <div>
            <div className="text-sm font-medium text-dark-100">Hit history — {targetName}</div>
            <div className="text-xs text-dark-500">
              {targetShortId ? <span className="font-mono text-violet-300">{targetShortId}</span> : null}
              {hits.length > 0 && <span className="ml-2">{hits.length} hits · {geocoded.length} with GPS</span>}
            </div>
          </div>
          <button
            onClick={onClose}
            className="text-dark-400 hover:text-dark-100 text-2xl leading-none px-2"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-5 flex-1 min-h-0">
          <div className="lg:col-span-3 min-h-[400px] h-full">
            <MapContainer
              center={initialCenter}
              zoom={geocoded.length === 0 ? 5 : 13}
              style={{ height: '100%', width: '100%', background: '#0f172a' }}
              scrollWheelZoom
            >
              <TileLayerControl position="topright" />
              <FitBounds hits={hits} />
              {geocoded.map((h, i) => (
                <Marker
                  key={h.id}
                  position={[h.latitude as number, h.longitude as number]}
                  icon={i === 0 ? latestIcon : olderIcon}
                >
                  <Popup>
                    <div>
                      <div style={{ color: '#a78bfa', fontWeight: 600 }}>
                        {new Date(h.createdAt).toLocaleString()}
                      </div>
                      <div>RSSI: {h.rssi != null ? `${h.rssi} dBm` : '—'}</div>
                      <div>MAC: {h.observedMac}</div>
                      <div>Node: {h.nodeId || '—'}</div>
                      {h.observedName && <div>Name: {h.observedName}</div>}
                    </div>
                  </Popup>
                </Marker>
              ))}
            </MapContainer>
          </div>

          <div className="lg:col-span-2 border-l border-dark-700/50 overflow-y-auto h-full">
            {isLoading && <div className="p-6 text-center text-sm text-dark-400">Loading hits...</div>}
            {error && <div className="p-6 text-center text-sm text-red-400">Failed to load hits</div>}
            {!isLoading && !error && hits.length === 0 && (
              <div className="p-6 text-center text-sm text-dark-400">No hits recorded yet for this target.</div>
            )}
            <ul className="divide-y divide-dark-700/30">
              {hits.map((h, i) => (
                <li key={h.id} className={`px-4 py-2 text-xs font-mono ${i === 0 ? 'bg-violet-500/5' : ''}`}>
                  <div className="flex items-baseline justify-between">
                    <span className="text-dark-200">{new Date(h.createdAt).toLocaleString()}</span>
                    <span className="text-dark-500">{h.rssi != null ? `${h.rssi} dBm` : ''}</span>
                  </div>
                  <div className="text-dark-400 truncate">{h.observedMac}{h.observedName ? ` · ${h.observedName}` : ''}</div>
                  <div className="text-dark-500 flex justify-between">
                    <span>{h.nodeId || '—'}</span>
                    <span>
                      {h.latitude != null && h.longitude != null
                        ? `${(h.latitude as number).toFixed(5)}, ${(h.longitude as number).toFixed(5)}`
                        : 'no GPS'}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </div>
    </div>
  )
}
