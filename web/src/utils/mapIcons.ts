import L from 'leaflet'

// ── SVG icon library ──

// Drone: top-down quadcopter (4 rotors + arms + body)
export const DRONE_SVG = `
  <circle cx="6" cy="6" r="4" fill="none" stroke-width="1.5"/>
  <circle cx="18" cy="6" r="4" fill="none" stroke-width="1.5"/>
  <circle cx="6" cy="18" r="4" fill="none" stroke-width="1.5"/>
  <circle cx="18" cy="18" r="4" fill="none" stroke-width="1.5"/>
  <line x1="6" y1="6" x2="12" y2="12" stroke-width="1.5"/>
  <line x1="18" y1="6" x2="12" y2="12" stroke-width="1.5"/>
  <line x1="6" y1="18" x2="12" y2="12" stroke-width="1.5"/>
  <line x1="18" y1="18" x2="12" y2="12" stroke-width="1.5"/>
  <circle cx="12" cy="12" r="2.5" fill="currentColor" stroke="none"/>
`
// WiFi: signal waves
export const WIFI_SVG = `<path d="M12 20h.01M8.53 16.11a6 6 0 018.94 0M5.64 12.72a10 10 0 0112.72 0M2.1 9.32a14 14 0 0119.8 0"/>`
// BLE: bluetooth symbol
export const BLE_SVG = `<path d="M7 7l10 10-5 5V2l5 5L7 17"/>`
// Person silhouette
export const PERSON_SVG = `<path d="M12 12a4 4 0 100-8 4 4 0 000 8zm0 2c-4 0-8 2-8 4v2h16v-2c0-2-4-4-8-4z"/>`
// Vehicle/car
export const VEHICLE_SVG = `<path d="M5 17h14M5 17a2 2 0 01-2-2v-3l2-5h10l2 5v3a2 2 0 01-2 2M5 17a2 2 0 002 2m10-2a2 2 0 002 2M7 13h.01M17 13h.01"/>`
// Unknown/untyped target: warning triangle
export const TARGET_SVG = `<path d="M12 9v4m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/>`
// Airplane (ADS-B)
export const AIRCRAFT_SVG = `<path d="M12 2L9 9H2l3 5-1 8 8-4 8 4-1-8 3-5h-7z"/>`
// Operator/pilot
export const OPERATOR_SVG = `<path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"/>`
// Deauth/attack: shield with lightning bolt
export const ATTACK_SVG = `<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M13 6l-3 6h4l-3 6" fill="none"/>`
// Mesh node: radio tower / antenna
export const NODE_SVG = `<path d="M12 20V10m0 0l-3-3m3 3l3-3"/><circle cx="12" cy="7" r="2"/><path d="M8.5 3.5a5 5 0 017 0M6 1a8.5 8.5 0 0112 0"/>`

// ── Threat level color scale ──
// Gray (baseline/unknown) → Green (friendly/low) → Orange (neutral/medium) → Red (hostile/high)
export const THREAT_COLORS = {
  none:     '#64748B', // gray — no threat / offline / unknown
  low:      '#22C55E', // green — friendly / low risk
  medium:   '#F59E0B', // orange — neutral / medium risk
  high:     '#EF4444', // red — hostile / high risk
  info:     '#3B82F6', // blue — informational (triangulating, active scanning)
}

// ── Drone status → threat color ──
export function droneStatusColor(status: string): string {
  switch (status) {
    case 'HOSTILE':  return THREAT_COLORS.high
    case 'FRIENDLY': return THREAT_COLORS.low
    case 'NEUTRAL':  return THREAT_COLORS.medium
    default:         return THREAT_COLORS.none  // UNKNOWN = gray baseline
  }
}

// ── Target status → color ──
export function targetStatusColor(status: string): string {
  switch (status) {
    case 'active':        return THREAT_COLORS.medium  // orange — needs attention
    case 'triangulating': return THREAT_COLORS.info    // blue — in progress
    case 'resolved':      return THREAT_COLORS.none    // gray — done
    default:              return THREAT_COLORS.medium
  }
}

// ── Node type → color (online/offline handled separately) ──
export function nodeColor(nodeType?: string, isOnline = true): string {
  if (!isOnline) return THREAT_COLORS.none
  if (nodeType === 'antihunter') return '#F97316' // orange for AH sensor nodes
  if (nodeType === 'gatesensor') return '#10B981' // emerald for gate sensors
  if (nodeType === 'operator') return '#94A3B8' // slate for plain Meshtastic operator handhelds
  return THREAT_COLORS.info // blue for GTM gateway nodes
}

// ── Target type → SVG icon ──
export function targetTypeSvg(targetType?: string): string {
  switch (targetType) {
    case 'wifi':    return WIFI_SVG
    case 'ble':     return BLE_SVG
    case 'drone':   return DRONE_SVG
    case 'person':  return PERSON_SVG
    case 'vehicle': return VEHICLE_SVG
    default:        return TARGET_SVG
  }
}

// ── CSS animation keyframes (injected once) ──
const STYLE_ID = 'map-icon-animations'
if (typeof document !== 'undefined' && !document.getElementById(STYLE_ID)) {
  const s = document.createElement('style')
  s.id = STYLE_ID
  s.textContent = `
    @keyframes icon-pulse { 0%,100% { opacity:1; box-shadow: 0 0 8px var(--pulse-color, #3B82F644); } 50% { opacity:0.7; box-shadow: 0 0 16px var(--pulse-color, #3B82F688); } }
    @keyframes icon-blink { 0%,100% { box-shadow: 0 0 4px var(--pulse-color, #3B82F644); } 50% { box-shadow: 0 0 14px var(--pulse-color, #3B82F6AA); } }
  `
  document.head.appendChild(s)
}

// ── Generic icon builder ──
export function makeIcon(
  svg: string, color: string, size: number,
  shape: 'circle' | 'square' | 'diamond' = 'circle',
  animation: 'none' | 'pulse' | 'blink' = 'none',
) {
  const border = shape === 'diamond' ? 'transform: rotate(45deg);' : shape === 'square' ? 'border-radius: 4px;' : 'border-radius: 50%;'
  const anim = animation === 'pulse' ? `animation: icon-pulse 2s ease-in-out infinite; --pulse-color: ${color}88;`
             : animation === 'blink' ? `animation: icon-blink 1.5s ease-in-out infinite; --pulse-color: ${color}AA;`
             : ''

  const svgEl = `<svg width="${size*0.5}" height="${size*0.5}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="color:${color}">${svg}</svg>`
  const inner = shape === 'diamond'
    ? `<div style="transform:rotate(-45deg);display:flex;align-items:center;justify-content:center;width:100%;height:100%">${svgEl}</div>`
    : svgEl

  return L.divIcon({
    className: '',
    html: `<div style="
      width:${size}px;height:${size}px;
      background:${color}18;border:2px solid ${color};
      ${border}
      display:flex;align-items:center;justify-content:center;
      box-shadow:0 0 8px ${color}44;
      ${anim}
    ">${inner}</div>`,
    iconSize: [size, size],
    iconAnchor: [size/2, size/2],
    popupAnchor: [0, -size/2],
  })
}

// ── Data-arrival pulse check ──
// Returns true if the entity received data within the pulse window.
export function shouldPulse(lastDataAt?: string, windowMs = 3000): boolean {
  if (!lastDataAt) return false
  return Date.now() - new Date(lastDataAt).getTime() < windowMs
}

// ── Specific icon factories ──

// Drone: circle, colored by threat. Pulses on fresh data, static otherwise.
export function droneMapIcon(status: string, pulsing = false) {
  return makeIcon(DRONE_SVG, droneStatusColor(status), 30, 'circle', pulsing ? 'pulse' : 'none')
}

// Target: diamond, typed SVG, colored by status. Pulses on fresh data.
export function targetMapIcon(targetType?: string, status?: string, pulsing = false) {
  return makeIcon(targetTypeSvg(targetType), targetStatusColor(status || 'active'), 26, 'diamond', pulsing ? 'pulse' : 'none')
}

// Aircraft (ADS-B): circle, purple
export function aircraftMapIcon() {
  return makeIcon(AIRCRAFT_SVG, '#A855F7', 24)
}

// Operator pin: square, yellow
export function operatorMapIcon() {
  return makeIcon(OPERATOR_SVG, '#F59E0B', 22, 'square')
}

// Mesh node: circle with antenna icon. Blinks on fresh data, static otherwise.
export function nodeMapIcon(nodeType?: string, isOnline = true, pulsing = false) {
  const color = nodeColor(nodeType, isOnline)
  return makeIcon(NODE_SVG, color, 24, 'circle', pulsing ? 'blink' : 'none')
}

// Attack event: diamond, red, always pulses
export function attackMapIcon() {
  return makeIcon(ATTACK_SVG, THREAT_COLORS.high, 26, 'diamond', 'pulse')
}
