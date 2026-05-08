// Typed wrappers around the /api/fleet-security backend (see
// internal/api/handlers_fleetsecurity.go and FLEET_SECURITY.md §3.4).
// Mirrors the api/client.ts pattern used by other domains.

import api from './client'

// ---- Types ----

export type IdentityRole = 'primary' | 'rescue' | 'operator' | 'revoked'
export type IdentitySource = 'auto-generated' | 'imported' | 'rotated'

export interface Identity {
  fingerprint: string
  label?: string
  role?: IdentityRole
  source?: IdentitySource
}

export interface IdentityRecord {
  id: string
  label: string
  fingerprint: string
  role: IdentityRole
  source: IdentitySource
  createdAt: string
  revokedAt?: string
  revokedReason?: string
  notes?: string
}

export type VerifyMethod = 'local-usb' | 'remote-pkc'
export type DriftStatus = 'unknown' | 'in-policy' | 'drift' | 'unreachable'

export interface NodeTrust {
  nodeNum: number
  nodeId?: string
  longName?: string
  shortName?: string
  sensorShortId?: string
  lastHeard?: string
  isOnline: boolean
  adminKeyFingerprints: string[]
  isManaged: boolean
  lastVerifiedAt?: string
  lastVerifyMethod?: VerifyMethod
  lastDriftCheckAt?: string
  driftStatus: DriftStatus
  notes?: string
}

export interface VerifyResult {
  nodeNum: number
  ok: boolean
  method?: VerifyMethod
  adminKeyFingerprints?: string[]
  isManaged?: boolean
  error?: string
}

export interface PubkeyExport {
  publicKeyB64: string
  fingerprint: string
}

// ---- Identity ----

export const fleetSecurityApi = {
  getIdentity: () => api.get<Identity>('/fleet-security/identity'),
  exportPubkey: () => api.get<PubkeyExport>('/fleet-security/identity/pubkey'),
  importIdentity: (label: string, privB64: string, pubB64: string) =>
    api.post<IdentityRecord>('/fleet-security/identity/import', {
      label, privB64, pubB64,
    }),

  // Registry
  listIdentities: () => api.get<IdentityRecord[]>('/fleet-security/identities'),
  registerIdentity: (label: string, pubB64: string, role: IdentityRole) =>
    api.post<IdentityRecord>('/fleet-security/identities', {
      label, pubB64, role,
    }),
  revokeIdentity: (fingerprint: string, reason: string) =>
    api.delete<{ revoked: boolean }>(
      `/fleet-security/identities/${encodeURIComponent(fingerprint)}`,
      { reason },
    ),

  // Trust roster
  listTrust: () => api.get<NodeTrust[]>('/fleet-security/trust'),
  getTrust: (nodeNum: number) =>
    api.get<NodeTrust>(`/fleet-security/trust/${nodeNum}`),
  verifyTrust: (nodeNum: number) =>
    api.post<VerifyResult>(`/fleet-security/trust/${nodeNum}/verify`),
  setAdminKeys: (
    nodeNum: number,
    keys: string[],
    opts?: { force?: boolean; ack?: string },
  ) =>
    api.put<{ applied: boolean }>(
      `/fleet-security/trust/${nodeNum}/admin-keys`,
      { keys, force: opts?.force ?? false, ack: opts?.ack ?? '' },
    ),
  setIsManaged: (
    nodeNum: number,
    value: boolean,
    opts?: { force?: boolean; ack?: string },
  ) =>
    api.put<{ applied: boolean }>(
      `/fleet-security/trust/${nodeNum}/is-managed`,
      { value, force: opts?.force ?? false, ack: opts?.ack ?? '' },
    ),
}

export default fleetSecurityApi
