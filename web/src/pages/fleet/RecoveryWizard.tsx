// RecoveryWizard walks the compromise-recovery flow. Three steps:
//   1. Confirm scenario + paste rescue keypair from cold storage.
//   2. Acknowledge consequences via TypedConfirm("RECOVER").
//   3. Watch the staged progress (install-rescue → push-fleet →
//      restore-primary → done) with live WS updates.
//
// On step 1, the operator must supply BOTH rescue privkey and pubkey
// (the backend verifies they match). Privkey bytes are sent over HTTPS
// to the local /fleet-security/recovery endpoint and zeroed in the Go
// process after the local Heltec install. They are never persisted.
//
// Failed targets in step 3 are listed; recovery proceeds to restore
// the local primary regardless so the local Heltec doesn't end up
// stuck on the rescue identity.

import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import fleetSecurityApi, {
  FLEET_SEC_RECOVERY_EVENT,
  type RecoveryProgressEvent,
  type RecoveryStage,
  type RecoveryStatus,
  type TargetStatus,
} from '../../api/fleetSecurity'
import wsClient from '../../api/websocket'
import TypedConfirm, { confirmed } from '../../components/TypedConfirm'
import { Field, ModalActions, ModalShell } from './IdentityImportModal'

interface Props {
  onClose: () => void
}

const stageLabel: Record<RecoveryStage, string> = {
  'install-rescue':  '1. Installing rescue identity on local Heltec',
  'push-fleet':      '2. Pushing new admin_key list to deployed nodes',
  'restore-primary': '3. Restoring fresh primary identity locally',
  done:              '✓ Recovery complete',
  failed:            '✗ Recovery failed',
}

const stageOrder: RecoveryStage[] = [
  'install-rescue',
  'push-fleet',
  'restore-primary',
  'done',
]

const targetStyles: Record<TargetStatus, string> = {
  pending:    'bg-dark-700/50 text-dark-300 border-dark-600/50',
  'in-flight':'bg-amber-700/30 text-amber-200 border-amber-600/40 animate-pulse',
  acked:      'bg-emerald-700/30 text-emerald-200 border-emerald-600/40',
  failed:     'bg-red-700/30 text-red-200 border-red-600/40',
}

export default function RecoveryWizard({ onClose }: Props) {
  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [privB64, setPrivB64] = useState('')
  const [pubB64, setPubB64] = useState('')
  const [newPrimaryLabel, setNewPrimaryLabel] = useState('')
  const [ack, setAck] = useState('')
  const [recoveryId, setRecoveryId] = useState<string | null>(null)

  const startM = useMutation({
    mutationFn: () =>
      fleetSecurityApi.startRecovery({
        rescuePrivB64: privB64.trim(),
        rescuePubB64: pubB64.trim(),
        ack,
        newPrimaryLabel: newPrimaryLabel.trim() || undefined,
      }),
    onSuccess: (res) => {
      setRecoveryId(res.recoveryId)
      setStep(3)
      // Wipe the privkey from React state -- the request body already
      // shipped it. JS can't actually zero memory, but at least we
      // don't keep it bound to a visible component prop.
      setPrivB64('')
    },
  })

  return (
    <ModalShell title="Disaster recovery" onClose={onClose}>
      <div className="bg-red-900/30 border border-red-700/40 rounded p-3 mb-5">
        <div className="text-xs text-red-200 font-semibold mb-1">
          ⚠ Critical operation
        </div>
        <p className="text-[11px] text-red-200/80">
          This wizard takes over the local Heltec's identity, mints a new
          primary keypair, and pushes the new admin_key list to every
          reachable deployed node. Mid-flight failures leave the local
          Heltec on the rescue identity until this wizard completes its
          final step. Audit log entries are tagged severity=critical.
        </p>
      </div>

      {step === 1 && (
        <>
          <Field label="Rescue public key (base64)">
            <textarea
              value={pubB64}
              onChange={(e) => setPubB64(e.target.value)}
              rows={2}
              spellCheck={false}
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
            />
          </Field>
          <div className="mt-3" />
          <Field label="Rescue private key (base64)">
            <textarea
              value={privB64}
              onChange={(e) => setPrivB64(e.target.value)}
              rows={2}
              spellCheck={false}
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
            />
            <p className="mt-1 text-[10px] text-dark-500">
              Read from your encrypted cold-storage backup. Sent to the
              local Heltec only; zeroed in the Go process after install.
            </p>
          </Field>
          <div className="mt-3" />
          <Field label="New primary label (optional)">
            <input
              type="text"
              value={newPrimaryLabel}
              onChange={(e) => setNewPrimaryLabel(e.target.value)}
              placeholder="defaults to cc-primary-recovered-<ts>"
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
            />
          </Field>
          <ModalActions
            onCancel={onClose}
            onSubmit={() => setStep(2)}
            submitLabel="Continue"
            submitDisabled={!privB64.trim() || !pubB64.trim()}
            destructive
          />
        </>
      )}

      {step === 2 && (
        <>
          <p className="text-xs text-dark-300 mb-3">
            One last confirmation. After you submit, the wizard begins
            installing the rescue keypair on the local Heltec and cannot
            be cancelled.
          </p>
          <ul className="text-[11px] text-dark-400 list-disc pl-5 space-y-1 mb-4">
            <li>Phone clients bonded to the previous identity will need to re-pair.</li>
            <li>Nodes offline during this run remain on their old admin_key list and need physical recovery.</li>
            <li>The cold-storage rescue keypair is unchanged; this only takes the local Heltec through it temporarily.</li>
            <li>The newly-minted primary keypair is generated on the backend; its privkey is also installed locally and not exposed to the UI.</li>
          </ul>
          <TypedConfirm
            phrase="RECOVER"
            value={ack}
            onChange={setAck}
          />
          {startM.error && (
            <div className="mt-3 text-xs text-red-300">
              {(startM.error as Error).message}
            </div>
          )}
          <ModalActions
            onCancel={() => setStep(1)}
            onSubmit={() => startM.mutate()}
            submitLabel="Begin recovery"
            submitDisabled={!confirmed('RECOVER', ack) || startM.isPending}
            pending={startM.isPending}
            destructive
          />
        </>
      )}

      {step === 3 && recoveryId && (
        <ProgressView recoveryId={recoveryId} onClose={onClose} />
      )}
    </ModalShell>
  )
}

// ProgressView is the live view of the recovery runner. Shows a
// step indicator + per-target status pills.
function ProgressView({
  recoveryId,
  onClose,
}: {
  recoveryId: string
  onClose: () => void
}) {
  const qc = useQueryClient()
  const { data } = useQuery<RecoveryStatus>({
    queryKey: ['fleet-security', 'recovery', recoveryId],
    queryFn: () => fleetSecurityApi.getRecovery(recoveryId),
    refetchInterval: (q) => (q.state.data?.completedAt ? false : 5000),
  })

  const [liveStage, setLiveStage] = useState<RecoveryStage | null>(null)
  const [liveErr, setLiveErr] = useState<string | null>(null)

  useEffect(() => {
    const handler = (payload: unknown) => {
      const evt = payload as RecoveryProgressEvent
      if (evt.recoveryId !== recoveryId) return
      setLiveStage(evt.stage)
      setLiveErr(evt.error ?? null)
      qc.setQueryData<RecoveryStatus | undefined>(
        ['fleet-security', 'recovery', recoveryId],
        (prev) => {
          if (!prev) return prev
          return {
            ...prev,
            stage: evt.stage,
            targets: evt.targets,
            completedAt: evt.done ? new Date().toISOString() : prev.completedAt,
          }
        },
      )
      if (evt.done) {
        qc.invalidateQueries({ queryKey: ['fleet-security', 'identity'] })
        qc.invalidateQueries({ queryKey: ['fleet-security', 'identities'] })
        qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
      }
    }
    wsClient.on(FLEET_SEC_RECOVERY_EVENT, handler)
    return () => wsClient.off(FLEET_SEC_RECOVERY_EVENT, handler)
  }, [recoveryId, qc])

  const currentStage = liveStage ?? data?.stage ?? 'install-rescue'
  const targets = data?.targets ?? []
  const acked = targets.filter((t) => t.status === 'acked').length
  const failed = targets.filter((t) => t.status === 'failed').length

  return (
    <div>
      <div className="space-y-1 mb-4">
        {stageOrder.map((s) => {
          const isActive = currentStage === s
          const isPast =
            stageOrder.indexOf(currentStage) > stageOrder.indexOf(s) ||
            currentStage === 'done'
          return (
            <div
              key={s}
              className={`text-xs px-3 py-1.5 rounded border ${
                isActive
                  ? 'bg-primary-900/30 border-primary-700/50 text-primary-200'
                  : isPast
                  ? 'bg-emerald-900/20 border-emerald-700/30 text-emerald-300'
                  : 'bg-dark-900/40 border-dark-700/50 text-dark-400'
              }`}
            >
              {isPast && !isActive ? '✓ ' : isActive ? '⟳ ' : '· '}
              {stageLabel[s]}
            </div>
          )
        })}
      </div>

      {currentStage === 'failed' && (
        <div className="bg-red-900/30 border border-red-700/40 rounded p-3 mb-4 text-xs text-red-200">
          {liveErr || 'Recovery failed -- see audit log for details.'}
        </div>
      )}

      {targets.length > 0 && (
        <div className="border-t border-dark-700/50 pt-3">
          <div className="text-[10px] uppercase tracking-wider text-dark-400 mb-2">
            Per-target push: {acked} acked, {failed} failed
          </div>
          <div className="space-y-1 max-h-48 overflow-y-auto">
            {targets.map((t) => (
              <div
                key={t.nodeNum}
                className={`flex items-center justify-between px-3 py-1.5 rounded border text-xs ${targetStyles[t.status]}`}
              >
                <span className="font-mono">!{t.nodeNum.toString(16).padStart(8, '0')}</span>
                <span className="uppercase tracking-wider text-[10px]">
                  {t.status}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      <ModalActions
        onCancel={onClose}
        onSubmit={onClose}
        submitLabel={data?.completedAt ? 'Close' : 'Continue in background'}
      />
    </div>
  )
}
