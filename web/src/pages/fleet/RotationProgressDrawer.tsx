// RotationProgressDrawer follows a single in-flight rotation. Subscribes
// to the WebSocket fleet-security.rotation.progress feed for live target
// status updates; falls back to GET /rotations/{id} for the initial
// snapshot (the WS feed only carries deltas after subscribe).
//
// Failed targets land in a Retry tray at the bottom; clicking Retry
// re-runs them via /rotations/{id}/retry which requires the operator
// to re-supply the PSK plaintext (we don't persist it).

import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import fleetSecurityApi, {
  FLEET_SEC_ROTATION_EVENT,
  type Rotation,
  type RotationPhase,
  type RotationProgressEvent,
  type RotationTarget,
  type TargetStatus,
} from '../../api/fleetSecurity'
import wsClient from '../../api/websocket'
import { ModalShell, ModalActions, Field } from './IdentityImportModal'

interface Props {
  rotationId: string
  // PSK plaintext for the retry path; passed through from the
  // RotatePSKModal that opened this drawer. Cleared when the drawer
  // closes.
  pskForRetry?: string
  onClose: () => void
}

const statusStyles: Record<TargetStatus, string> = {
  pending:    'bg-dark-700/50 text-dark-300 border-dark-600/50',
  'in-flight':'bg-amber-700/30 text-amber-200 border-amber-600/40 animate-pulse',
  acked:      'bg-emerald-700/30 text-emerald-200 border-emerald-600/40',
  failed:     'bg-red-700/30 text-red-200 border-red-600/40',
}

// Per-phase styling for the five-step staged-rotation indicator. Used
// by the stepper rendered in each target row -- each step's dot reads
// its own colour from this map based on whether the target is past,
// at, or before that phase. Failed phases get red dots that bleed
// into the next step's color so the failure is visually anchored.
const stepLabels: { id: 'B' | 'C'; label: string; helpText: string }[] = [
  {
    id: 'B',
    label: 'B push',
    helpText: 'Pi pushes new PSK to the remote on a SECONDARY channel slot',
  },
  {
    id: 'C',
    label: 'C promote',
    helpText: 'Remote promotes the staged SECONDARY to PRIMARY',
  },
]

// stepStateFor renders one of {pending, active, ok, fail, skip} for a
// step based on the target's current phase.
function stepStateFor(phase: RotationPhase | undefined, step: 'B' | 'C'): 'pending' | 'active' | 'ok' | 'fail' | 'skip' {
  const p = phase ?? 'pending'
  if (step === 'B') {
    if (p === 'pending') return 'pending'
    if (p === 'phase_b_pushing') return 'active'
    if (p === 'failed_b') return 'fail'
    // anything past Phase B (has_new_psk, phase_c_promoting, on_new_psk,
    // failed_c, retired) means B succeeded.
    return 'ok'
  }
  // step === 'C'
  if (p === 'failed_b') return 'skip' // never got to C
  if (p === 'pending' || p === 'phase_b_pushing' || p === 'has_new_psk') return 'pending'
  if (p === 'phase_c_promoting') return 'active'
  if (p === 'failed_c') return 'fail'
  // on_new_psk / retired
  return 'ok'
}

const stepColors: Record<'pending' | 'active' | 'ok' | 'fail' | 'skip', string> = {
  pending: 'bg-dark-700/40 text-dark-500 border-dark-600/30',
  active:  'bg-amber-700/30 text-amber-200 border-amber-600/50 animate-pulse',
  ok:      'bg-emerald-700/30 text-emerald-200 border-emerald-600/50',
  fail:    'bg-red-700/30 text-red-200 border-red-600/50',
  skip:    'bg-dark-800/60 text-dark-600 border-dark-700/30',
}

function effectivePhase(t: RotationTarget): RotationPhase {
  // Server emits both phase + status; phase is the source of truth.
  // Older rotation rows pre-000027 only have status; back-fill here so
  // the stepper renders sensibly (mostly the on_new_psk / failed_b
  // collapses).
  if (t.phase) return t.phase
  switch (t.status) {
    case 'acked':
      return 'on_new_psk'
    case 'failed':
      return 'failed_b'
    case 'in-flight':
      return 'phase_b_pushing'
    default:
      return 'pending'
  }
}

export default function RotationProgressDrawer({
  rotationId,
  pskForRetry,
  onClose,
}: Props) {
  const qc = useQueryClient()
  const { data, refetch } = useQuery<Rotation>({
    queryKey: ['fleet-security', 'rotation', rotationId],
    queryFn: () => fleetSecurityApi.getRotation(rotationId),
    refetchInterval: (q) => (q.state.data?.completedAt ? false : 5000),
  })

  // Subscribe to WS progress events for THIS rotation -- updates land
  // immediately rather than waiting for the 5s poll.
  useEffect(() => {
    const handler = (payload: unknown) => {
      const evt = payload as RotationProgressEvent
      if (evt.rotationId !== rotationId) return
      // Patch the cached rotation in place so the UI updates without
      // a network round-trip.
      qc.setQueryData<Rotation | undefined>(
        ['fleet-security', 'rotation', rotationId],
        (prev) => {
          if (!prev) return prev
          return {
            ...prev,
            targets: evt.targets,
            completedAt: evt.done ? new Date().toISOString() : prev.completedAt,
            newPskFingerprint: evt.newPskFingerprint || prev.newPskFingerprint,
          }
        },
      )
      // Also refresh the channels card so the new fingerprint shows.
      if (evt.done) {
        qc.invalidateQueries({ queryKey: ['fleet-security', 'channels'] })
        qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
      }
    }
    wsClient.on(FLEET_SEC_ROTATION_EVENT, handler)
    return () => wsClient.off(FLEET_SEC_ROTATION_EVENT, handler)
  }, [rotationId, qc])

  const retryM = useMutation({
    mutationFn: ({ targets, pskB64 }: { targets: number[]; pskB64?: string }) =>
      fleetSecurityApi.retryRotation(rotationId, targets, pskB64),
    onSuccess: () => {
      refetch()
    },
  })

  const failed = (data?.targets ?? []).filter((t) => t.status === 'failed')
  const acked = (data?.targets ?? []).filter((t) => t.status === 'acked').length
  const total = data?.targets?.length ?? 0
  const inFlight = (data?.targets ?? []).filter((t) => t.status === 'in-flight').length
  const done = !!data?.completedAt

  // If the operator typed/pasted the PSK at modal-open time, allow
  // bulk-retry of all failed nodes. Otherwise show a paste box.
  const [retryPSK, setRetryPSK] = useState(pskForRetry ?? '')

  return (
    <ModalShell
      title={`Rotation ${rotationId.slice(0, 8)} — channel ${data?.channelIndex ?? '?'}`}
      onClose={onClose}
    >
      {!data && <div className="text-sm text-dark-400">Loading…</div>}

      {data && (
        <>
          <div className="mb-3 text-xs text-dark-300 flex items-center gap-3">
            <span className={done ? 'text-emerald-300' : 'text-amber-300'}>
              {done ? '✓ Done' : `⟳ ${inFlight} in-flight`}
            </span>
            <span className="text-dark-500">·</span>
            <span>{acked}/{total} acked</span>
            {data.newPskFingerprint && (
              <>
                <span className="text-dark-500">·</span>
                <span className="font-mono text-[10px] text-dark-400">
                  new psk fp: {data.newPskFingerprint}
                </span>
              </>
            )}
          </div>

          <div className="space-y-1 max-h-72 overflow-y-auto">
            {data.targets.map((t) => {
              const phase = effectivePhase(t)
              return (
                <div
                  key={t.nodeNum}
                  className={`flex items-center justify-between gap-3 px-3 py-1.5 rounded border text-xs ${statusStyles[t.status]}`}
                >
                  <span className="font-mono shrink-0">!{t.nodeNum.toString(16).padStart(8, '0')}</span>
                  <div className="flex items-center gap-1.5 shrink-0">
                    {stepLabels.map((step) => {
                      const state = stepStateFor(phase, step.id)
                      return (
                        <span
                          key={step.id}
                          title={`${step.label}: ${step.helpText}`}
                          className={`px-1.5 py-0.5 rounded border text-[9px] uppercase tracking-wider ${stepColors[state]}`}
                        >
                          {step.id}
                        </span>
                      )
                    })}
                  </div>
                  <span className="flex items-center gap-2 ml-auto">
                    {t.attempts > 0 && (
                      <span className="text-[10px] opacity-70">
                        attempt {t.attempts}
                      </span>
                    )}
                    <span className="uppercase tracking-wider text-[10px]">
                      {phase.replace(/_/g, ' ')}
                    </span>
                  </span>
                </div>
              )
            })}
          </div>

          {failed.length > 0 && (
            <div className="mt-4 pt-4 border-t border-dark-700/50 space-y-2">
              <div className="text-xs text-amber-200 font-semibold">
                {failed.length} failed target{failed.length === 1 ? '' : 's'}
              </div>
              {failed.map((t) => {
                const phase = effectivePhase(t)
                const tag =
                  phase === 'failed_b' ? 'phase B' :
                  phase === 'failed_c' ? 'phase C' : 'phase ?'
                return (
                  <div key={t.nodeNum} className="text-[11px] text-red-300/80 font-mono">
                    !{t.nodeNum.toString(16).padStart(8, '0')} ({tag}): {t.lastError}
                  </div>
                )
              })}
              <Field label="Override PSK (optional, base64 — 16 or 32 bytes)">
                <input
                  type="text"
                  value={retryPSK}
                  onChange={(e) => setRetryPSK(e.target.value)}
                  spellCheck={false}
                  placeholder="leave blank to use the rotation's stored PSK"
                  className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
                />
                <p className="mt-1 text-[10px] text-dark-500">
                  Backend stashes the new PSK on rotation-start and clears it once every
                  target acks; while any target is failed the retry uses the stored value
                  automatically. Override only for fully-acked rotations or to send a
                  different PSK.
                </p>
              </Field>
              <button
                type="button"
                disabled={retryM.isPending}
                onClick={() =>
                  retryM.mutate({
                    targets: failed.map((t) => t.nodeNum),
                    pskB64: retryPSK.trim() || undefined,
                  })
                }
                className="px-3 py-1.5 rounded bg-amber-600 hover:bg-amber-500 disabled:bg-dark-700 disabled:text-dark-500 text-xs text-white"
              >
                {retryM.isPending ? 'Retrying…' : `Retry ${failed.length} target${failed.length === 1 ? '' : 's'}`}
              </button>
              {retryM.error && (
                <div className="text-xs text-red-300">
                  {(retryM.error as Error).message}
                </div>
              )}
            </div>
          )}
        </>
      )}

      <ModalActions
        onCancel={onClose}
        onSubmit={onClose}
        submitLabel={done ? 'Close' : 'Continue in background'}
      />
    </ModalShell>
  )
}
