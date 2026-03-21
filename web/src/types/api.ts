// Centralized API types — matches backend JSON responses

// Drone types
export interface Drone {
  id: string
  droneId: string
  mac?: string
  serialNumber?: string
  uasId?: string
  operatorId?: string
  uaType?: string
  manufacturer?: string
  model?: string
  lat: number
  lon: number
  altitude?: number
  speed?: number
  heading?: number
  verticalSpeed?: number
  operatorLat?: number
  operatorLon?: number
  rssi?: number
  status: 'UNKNOWN' | 'FRIENDLY' | 'NEUTRAL' | 'HOSTILE'
  source?: string
  nodeId?: string
  siteId?: string
  originSiteId?: string
  siteName?: string
  siteColor?: string
  siteCountry?: string
  siteCity?: string
  faa?: Record<string, unknown>
  ts?: string
  firstSeen?: string
  lastSeen?: string
}

// Node types
export interface MeshNode {
  id: string
  nodeNum: number
  name?: string
  shortName?: string
  hwModel?: string
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
  snr?: number
  rssi?: number
  ts: string
  lastHeard: string
  lastSeen?: string
  isOnline: boolean
  siteId?: string
  originSiteId?: string
  siteName?: string
  siteColor?: string
  siteCountry?: string
  siteCity?: string
  lastMessage?: string
  temperatureUpdatedAt?: string
}

// Alert types
export interface AlertRule {
  id: string
  name: string
  description?: string
  condition: Record<string, unknown>
  severity: string
  enabled: boolean
  cooldownSeconds: number
  lastTriggered?: string
}

export interface AlertEvent {
  id: string
  ruleId?: string
  severity: string
  title: string
  message?: string
  data?: Record<string, unknown>
  acknowledged: boolean
  acknowledgedBy?: string
  acknowledgedAt?: string
  createdAt: string
}

// Geofence
export interface Geofence {
  id: string
  name: string
  description?: string
  color?: string
  polygon: { lat: number; lng: number }[]
  action: string
  enabled: boolean
  alarmEnabled: boolean
  alarmLevel?: string
  alarmMessage?: string
  triggerOnEntry: boolean
  triggerOnExit: boolean
  appliesToAdsb: boolean
  appliesToDrones: boolean
  appliesToTargets: boolean
  appliesToDevices: boolean
  siteId?: string
}

// Target
export interface Target {
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

// Chat
export interface ChatMessage {
  id: string
  fromNode: number
  toNode: number
  channel: number
  text: string
  timestamp: string
}

// ADS-B
export interface Aircraft {
  hex: string
  flight?: string
  altBaro?: number
  gs?: number
  track?: number
  squawk?: string
  lat?: number
  lon?: number
  rssi?: number
  messages?: number
  seen?: number
}

// Command
export interface Command {
  id: string
  targetNode: number
  commandType: string
  payload?: Record<string, unknown>
  status: string
  sentAt?: string
  ackedAt?: string
  result?: Record<string, unknown>
  retryCount: number
  maxRetries: number
  createdAt: string
}

// Auth
export interface User {
  id: string
  email: string
  name?: string
  role: string
  twoFactorEnabled?: boolean
}

export interface AuthResponse {
  token: string
  user: User
  legalAccepted: boolean
  twoFactorRequired: boolean
  disclaimer?: string
}

// Text message (serial ring buffer)
export interface TextMessage {
  seq: number
  nodeId: string
  message: string
  timestamp: string
  siteId?: string
}

// WebSocket event
export interface WSEvent<T = unknown> {
  type: string
  payload: T
}
