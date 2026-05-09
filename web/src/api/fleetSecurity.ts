// Typed wrappers around the /api/fleet-security backend (see
// internal/api/handlers_fleetsecurity.go).
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
  // PSK fingerprint the node was last confirmed on (= local PRIMARY's
  // fp at the moment of a successful Verify round-trip). Compared
  // against the channel's current fingerprint to detect "old psk" /
  // "migration lagging" trust rows during a staged rotation. Empty
  // means "never verified since 000028".
  currentPskFp?: string
  // PSK fingerprint the node was last on before the most recent
  // rotation that stranded it. Pairs with a row in fleet_recovery_psks
  // (same fp) holding the actual PSK bytes the recover_stranded job
  // needs. Migration 000030.
  previousPskFp?: string
  strandedSince?: string
  recoveryAttempts?: number
  lastRecoveryAt?: string
  lastRecoveryError?: string
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

  // Channels + PSK rotation
  listChannels: () => api.get<Channel[]>('/fleet-security/channels'),
  rotatePSK: (
    channelIndex: number,
    body: {
      source: 'random' | 'explicit'
      pskB64?: string
      targets?: number[]
      ack: string
      notes?: string
      interTargetDelayMs?: number
    },
  ) =>
    api.post<{ rotationId: string }>(
      `/fleet-security/channels/${channelIndex}/rotate`,
      body,
    ),
  getRotation: (id: string) =>
    api.get<Rotation>(`/fleet-security/rotations/${id}`),
  // pskB64 is optional. The backend stashes the new PSK on rotation-start
  // and clears it once every target is acked, so retries against a still-
  // failed rotation work without an operator-supplied paste. Pass a value
  // only for fully-acked rotations (rare) or to override.
  retryRotation: (id: string, targets: number[], pskB64?: string) =>
    api.post<{ queued: boolean }>(
      `/fleet-security/rotations/${id}/retry`,
      pskB64 ? { pskB64, targets } : { targets },
    ),

  // Phase E retirement of a completed staged rotation. ack must be
  // "RETIRE" (typed-confirmation gate, the same way RotatePSK requires
  // "ROTATE"). Returns 409 with laggards on gate failure -- callers
  // should render the laggards list as "still pending" before
  // re-enabling the Retire button.
  retireOldPSK: (id: string) =>
    api.post<RetireOldPSKResult>(
      `/fleet-security/rotations/${id}/retire-old-psk`,
      { ack: 'RETIRE' },
    ),

  // Recovery (legacy CC-PRO compromised-identity flow, distinct from
  // post-rotation stranded-node recovery below).
  startRecovery: (body: {
    rescuePrivB64: string
    rescuePubB64: string
    ack: string
    newPrimaryLabel?: string
    notes?: string
  }) =>
    api.post<{ recoveryId: string }>('/fleet-security/recovery', body),
  getRecovery: (id: string) =>
    api.get<RecoveryStatus>(`/fleet-security/recovery/${id}`),

  // Stranded nodes (post-rotation recovery). The dispatcher hook +
  // recover_stranded job handler do the actual work; the UI surfaces
  // who's stranded and gives the operator force-recover + give-up
  // affordances.
  listStranded: () => api.get<NodeTrust[]>('/fleet-security/stranded'),
  recoverStrandedNow: (nodeNum: number) =>
    api.post<{ jobId: string }>(
      `/fleet-security/stranded/${nodeNum}/recover-now`,
    ),
  cancelStranded: (nodeNum: number) =>
    api.delete<{ ok: boolean }>(`/fleet-security/stranded/${nodeNum}`),
}

// ---- Channels + Rotations types ----

export type ChannelRole = 'PRIMARY' | 'SECONDARY' | 'DISABLED'

export interface Channel {
  index: number
  name: string
  role: ChannelRole
  pskFingerprint?: string
  pskLength: number
  lastRotatedAt?: string
  lastRotatedBy?: string
  lastRotationId?: string
}

export type RotationKind = 'psk' | 'identity' | 'admin-keys' | 'recovery'
// Legacy 4-state status, kept for backward-compat with older Rotation
// rows (pre-migration 000027). New code reads `phase`.
export type TargetStatus = 'pending' | 'in-flight' | 'acked' | 'failed'
// Per-target staged rotation phases.
//
//   pending           -> phase_b_pushing  -> has_new_psk
//                                          -> failed_b
//   has_new_psk       -> phase_c_promoting -> on_new_psk
//                                          -> failed_c
//   on_new_psk        -> retired (after Phase E)
//
// failed_b: Phase B push failed; remote stays on PSK_OLD only,
// reachable. Retry from pending re-runs Phase B.
// failed_c: Phase B succeeded, Phase C didn't; remote has both
// channels active. Retry from has_new_psk runs only Phase C.
export type RotationPhase =
  | 'pending'
  | 'phase_b_pushing'
  | 'has_new_psk'
  | 'phase_c_promoting'
  | 'on_new_psk'
  | 'retired'
  | 'failed_b'
  | 'failed_c'

export type PiLocalPhase =
  | 'pending'
  | 'staging_added'
  | 'phase_d_promoted'
  | 'retired'

export interface RotationTarget {
  nodeNum: number
  phase?: RotationPhase
  status: TargetStatus
  attempts: number
  lastError?: string
}

export interface Rotation {
  id: string
  kind: RotationKind
  channelIndex?: number
  stagingChannelIndex?: number
  piLocalPhase?: PiLocalPhase
  startedBy?: string
  startedAt: string
  completedAt?: string
  retiredAt?: string
  targets: RotationTarget[]
  newPskFingerprint?: string
  notes?: string
}

// Result of POST /rotations/{id}/retire-old-psk. ok=false + non-empty
// laggards means the gate (every managed-trust row's current_psk_fp ==
// rotation's new fp) failed; the UI shows the laggards as "still
// pending: <node-nums>" until they're verified on the new PSK.
export interface RetireOldPSKResult {
  ok: boolean
  laggards?: number[]
  oldChannelIndex?: number
  newPskFingerprint?: string
}

// WebSocket event payload from EventFleetSecRotation.
export interface RotationProgressEvent {
  rotationId: string
  kind: RotationKind
  targets: RotationTarget[]
  done: boolean
  newPskFingerprint?: string
  // Optional human-readable status line emitted at key rotation
  // milestones. The drawer's status rail shows the most recent notice
  // so operators see what the worker is doing without tailing logs.
  notice?: string
}

export const FLEET_SEC_ROTATION_EVENT = 'fleet-security.rotation.progress'

// ---- Recovery types ----

export type RecoveryStage =
  | 'install-rescue'
  | 'push-fleet'
  | 'restore-primary'
  | 'done'
  | 'failed'

export interface RecoveryStatus {
  id: string
  stage: RecoveryStage
  startedAt: string
  completedAt?: string
  newPrimaryFingerprint: string
  rescueFingerprint?: string
  oldPrimaryFingerprint?: string
  targets: RotationTarget[]
  notes?: string
}

export interface RecoveryProgressEvent {
  recoveryId: string
  stage: RecoveryStage
  targets: RotationTarget[]
  done: boolean
  error?: string
}

export const FLEET_SEC_RECOVERY_EVENT = 'fleet-security.recovery.progress'

export default fleetSecurityApi
