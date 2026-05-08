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

// Per-phase styling for the rotation stepper. Reflects the new
// 3-phase atomic flow: Phase B is one atomic transaction per remote
// that does the install + promote + old-PSK-wipe in a single firmware
// flash write; Phase C is the operator-paced Pi-local migration that
// runs when Retire is clicked. The rotation worker never enters
// has_new_psk or phase_c_promoting in the new design, so step C
// transitions straight from pending to ok in one tick for remotes.
// For the Pi-local target the per-row stepper stays at "C-pending"
// throughout Phase B and only flips to "C-ok" once Retire runs.
const stepLabels: { id: 'B' | 'C'; label: string; helpText: string }[] = [
  {
    id: 'B',
    label: 'B migrate (atomic)',
    helpText: 'Atomic transaction on the remote: begin → SetChannel(staging, PRIMARY, new) → SetChannel(old, DISABLED) → commit. ~5-10s commit-side latency over PKC.',
  },
  {
    id: 'C',
    label: 'C Pi promote',
    helpText: 'Operator-paced. Runs when you click "Retire old PSK". Same atomic transaction on Pi-local: promotes staging to PRIMARY, wipes old PSK in one flash write.',
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

  // notices is a rolling buffer of recent status lines emitted by the
  // backend rotation worker (Phase 0/A/B/C/D milestones, picker
  // decisions, retry hints). Bottom-left status rail renders these.
  // Newest at index 0; capped at 8 entries to bound re-renders.
  const [notices, setNotices] = useState<{ ts: number; msg: string }[]>([])

  // Subscribe to WS progress events for THIS rotation -- updates land
  // immediately rather than waiting for the 5s poll.
  useEffect(() => {
    const handler = (payload: unknown) => {
      const evt = payload as RotationProgressEvent
      if (evt.rotationId !== rotationId) return
      // Notice-only event: backend emits these at phase boundaries.
      if (evt.notice) {
        setNotices((prev) => {
          const next = [{ ts: Date.now(), msg: evt.notice as string }, ...prev]
          return next.slice(0, 8)
        })
      }
      // Patch the cached rotation in place so the UI updates without
      // a network round-trip. Notice-only events still carry targets
      // (a snapshot at notice time), so this is safe to apply
      // unconditionally.
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

  // Inline Phase C trigger. The Retire-now button on the Pi-local row
  // calls this directly. On success the bottom action label flips from
  // "Continue in background" to "Job finished, close now" so the
  // operator gets a clear "you can close this" signal.
  const retireM = useMutation({
    mutationFn: () => fleetSecurityApi.retireOldPSK(rotationId),
    onSuccess: () => {
      // Refresh everything that depends on the post-retire state.
      qc.invalidateQueries({ queryKey: ['fleet-security', 'rotation', rotationId] })
      qc.invalidateQueries({ queryKey: ['fleet-security', 'pending-retirements'] })
      qc.invalidateQueries({ queryKey: ['fleet-security', 'channels'] })
      qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
    },
  })
  const retireSucceeded = retireM.isSuccess && retireM.data?.ok === true

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

          {(() => null)() /* gate state computed below; isolated for readability */}
          {/* Retirement gate is open when every non-pending target has
              already reached on_new_psk (or retired). In the new flow
              that means every managed remote has been atomically
              migrated by Phase B, so the only thing left is Pi-local
              Phase C. Once the gate is open, the Pi-local row's badge
              switches from "waiting for retire" to "ready for retire"
              with a clickable Retire-now button (when the parent wired
              onTriggerRetire). */}
          <div className="space-y-1 max-h-72 overflow-y-auto">
            {(() => {
              // Hoist gate computation so each row reuses it.
              const nonPiTargets = data.targets.filter(
                (t) => effectivePhase(t) !== 'pending',
              )
              const allNonPiOnNew = nonPiTargets.length > 0 && nonPiTargets.every((t) => {
                const p = effectivePhase(t)
                return p === 'on_new_psk' || p === 'retired'
              })
              const gateOpen =
                data.piLocalPhase === 'staging_added' && allNonPiOnNew
              return data.targets.map((t) => {
              const phase = effectivePhase(t)
              // Pi-local detection in the 3-phase atomic flow: when
              // pi_local_phase is staging_added (Phase A done, Phase B
              // walking remotes) and a target is still at phase=pending,
              // that target is almost certainly the Pi-local node
              // waiting for the operator to click Retire (Phase C). We
              // don't have a strict nodeNum=localNodeNum check here
              // because the drawer doesn't fetch identity, but the
              // (staging_added && pending) signal is unambiguous: every
              // managed remote has been atomically migrated by Phase B,
              // so anything stuck at pending is the deliberate
              // operator-paced Pi-local target.
              const isWaitingForRetire =
                data.piLocalPhase === 'staging_added' &&
                phase === 'pending'
              const isReadyForRetire = isWaitingForRetire && gateOpen
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
                    {isReadyForRetire ? (
                      <>
                        <span
                          title="Every reachable managed remote is on the new PSK. Pi-local can promote now."
                          className="uppercase tracking-wider text-[10px] text-emerald-300"
                        >
                          ready for retire
                        </span>
                        <button
                          type="button"
                          disabled={retireM.isPending}
                          onClick={() => retireM.mutate()}
                          title="Phase C: atomic transaction on Pi-local promotes staging to PRIMARY and wipes old PSK in one flash write"
                          className="px-2 py-0.5 rounded border border-amber-600/40 bg-amber-700/20 hover:bg-amber-700/40 disabled:bg-dark-700 disabled:border-dark-600 disabled:text-dark-500 text-[10px] text-amber-100"
                        >
                          {retireM.isPending ? 'Retiring…' : 'Retire now'}
                        </button>
                      </>
                    ) : isWaitingForRetire ? (
                      <span
                        title="The rotation worker has finished migrating every reachable remote. The Pi local Heltec migrates last, when you click Retire old PSK below. Until then Pi keeps both old + new PSK so any laggard that comes online can still be caught up via the still-active old channel."
                        className="uppercase tracking-wider text-[10px] text-amber-300"
                      >
                        waiting for retire
                      </span>
                    ) : (
                      <span className="uppercase tracking-wider text-[10px]">
                        {phase.replace(/_/g, ' ')}
                      </span>
                    )}
                  </span>
                </div>
              )
            })
            })()}
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

      {/* Status rail · live worker notices in newest-first order. The
          most recent line is rendered prominently; older lines fade
          to muted text. Empty until the first notice arrives. Fixed
          height keeps the modal layout stable across rotations. */}
      <div className="mt-4 pt-3 border-t border-dark-700/40 min-h-[3.5rem]">
        {notices.length === 0 ? (
          <div className="text-[10px] uppercase tracking-wider text-dark-600">
            waiting for worker…
          </div>
        ) : (
          <div className="space-y-0.5">
            <div className="text-[11px] text-emerald-300/90 font-mono">
              ▶ {notices[0].msg}
            </div>
            {notices.slice(1, 4).map((n) => (
              <div key={n.ts} className="text-[10px] text-dark-500 font-mono opacity-70">
                · {n.msg}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Bottom action label flips based on lifecycle state:
          - retire just succeeded -> emphatic "Job finished, close now"
          - rotation worker hit done flag (Phase B finished, Phase C
            may still be pending operator click) -> "Close"
          - otherwise -> "Continue in background" so operator knows it
            keeps running after they leave the modal. */}
      <ModalActions
        onCancel={onClose}
        onSubmit={onClose}
        submitLabel={
          retireSucceeded
            ? 'Job finished, close now'
            : done
              ? 'Close'
              : 'Continue in background'
        }
      />
    </ModalShell>
  )
}
