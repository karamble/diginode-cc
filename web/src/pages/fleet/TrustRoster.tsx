// TrustRoster renders the per-node trust table -- the heart of the
// Fleet Security tab. Each row shows last-seen, the admin_key list as
// labeled chips, is_managed status, and a trust-health pill. Per-row
// actions: Verify, Edit admin keys.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import fleetSecurityApi, {
  type IdentityRecord,
  type NodeTrust,
  type VerifyResult,
} from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'
import PubkeyChip from '../../components/PubkeyChip'
import TrustHealthPill from '../../components/TrustHealthPill'
import EditAdminKeysModal from './EditAdminKeysModal'

type RowFeedback = { kind: 'ok' | 'err'; text?: string }

const FEEDBACK_MS = 4000

function lastVerifiedClass(lastVerifiedAt?: string): string {
  if (!lastVerifiedAt) return 'text-dark-500'
  const ageMs = Date.now() - new Date(lastVerifiedAt).getTime()
  const days = ageMs / 86_400_000
  if (days < 1) return 'text-dark-400'
  if (days < 3) return 'text-amber-300'
  if (days < 7) return 'text-orange-300'
  return 'text-red-300'
}

export default function TrustRoster() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'
  const qc = useQueryClient()

  const { data: nodes, isLoading, error } = useQuery<NodeTrust[]>({
    queryKey: ['fleet-security', 'trust'],
    queryFn: () => fleetSecurityApi.listTrust(),
  })

  const { data: registry } = useQuery<IdentityRecord[]>({
    queryKey: ['fleet-security', 'identities'],
    queryFn: () => fleetSecurityApi.listIdentities(),
  })

  // Fleet PRIMARY fingerprint -- used by TrustHealthPill to detect a
  // node still on a stale PSK after a staged rotation. listChannels is
  // already cached in ChannelsCard so this is usually a free lookup.
  const { data: channels } = useQuery<Awaited<ReturnType<typeof fleetSecurityApi.listChannels>>>({
    queryKey: ['fleet-security', 'channels'],
    queryFn: () => fleetSecurityApi.listChannels(),
  })
  const fleetPrimaryFp = channels?.find((c) => c.index === 0)?.pskFingerprint

  const labelByFp = new Map<string, IdentityRecord>()
  for (const r of registry ?? []) labelByFp.set(r.fingerprint, r)

  // Per-row pending + transient feedback. We deliberately do NOT disable
  // the Verify button when a row is in flight -- the operator may want
  // to re-trigger at any time. We just show a spinner so the click
  // registers visually.
  const [pending, setPending] = useState<Set<number>>(() => new Set())
  const [feedback, setFeedback] = useState<Map<number, RowFeedback>>(() => new Map())
  const feedbackTimers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map())

  useEffect(() => {
    const timers = feedbackTimers.current
    return () => {
      for (const t of timers.values()) clearTimeout(t)
      timers.clear()
    }
  }, [])

  function setRowFeedback(nodeNum: number, fb: RowFeedback) {
    setFeedback((prev) => {
      const next = new Map(prev)
      next.set(nodeNum, fb)
      return next
    })
    const existing = feedbackTimers.current.get(nodeNum)
    if (existing) clearTimeout(existing)
    const timer = setTimeout(() => {
      setFeedback((prev) => {
        const next = new Map(prev)
        next.delete(nodeNum)
        return next
      })
      feedbackTimers.current.delete(nodeNum)
    }, FEEDBACK_MS)
    feedbackTimers.current.set(nodeNum, timer)
  }

  const verifyM = useMutation<VerifyResult, Error, number>({
    mutationFn: (nodeNum: number) => fleetSecurityApi.verifyTrust(nodeNum),
  })

  function runVerify(nodeNum: number) {
    setPending((prev) => {
      const next = new Set(prev)
      next.add(nodeNum)
      return next
    })
    verifyM.mutate(nodeNum, {
      onSuccess: (result) => {
        // Backend returns 200 even on mesh failure; the ok flag in the
        // body distinguishes success from a failed round-trip.
        if (result.ok) {
          setRowFeedback(nodeNum, { kind: 'ok' })
        } else {
          setRowFeedback(nodeNum, {
            kind: 'err',
            text: result.error || 'verify failed',
          })
        }
        qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
      },
      onError: (err) => {
        setRowFeedback(nodeNum, {
          kind: 'err',
          text: err.message || 'request failed',
        })
      },
      onSettled: () => {
        setPending((prev) => {
          const next = new Set(prev)
          next.delete(nodeNum)
          return next
        })
      },
    })
  }

  const [editing, setEditing] = useState<NodeTrust | null>(null)

  return (
    <section className="bg-dark-800/60 border border-dark-700/50 rounded-lg p-5">
      <header className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-sm font-semibold text-dark-100">Trust roster</h3>
          <p className="text-xs text-dark-400 mt-0.5">
            Per-node admin_key list + is_managed state. Verify pulls fresh
            SecurityConfig over the mesh (PKC) or local USB if it's the
            local Heltec.
          </p>
        </div>
        <button
          type="button"
          onClick={() => qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })}
          className="text-xs text-dark-400 hover:text-dark-200"
        >
          ↻ Refresh
        </button>
      </header>

      {isLoading && <div className="text-sm text-dark-400">Loading roster…</div>}
      {error && (
        <div className="text-xs text-red-300">
          {(error as Error).message}
        </div>
      )}

      {nodes && nodes.length === 0 && (
        <div className="text-xs text-dark-500 italic">
          No nodes in the database yet. Connect a Heltec and let it populate
          the node list before configuring trust.
        </div>
      )}

      {nodes && nodes.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-dark-400 border-b border-dark-700/50">
                <th className="py-2 pr-3 font-medium">Node</th>
                <th className="py-2 pr-3 font-medium">Health</th>
                <th className="py-2 pr-3 font-medium">Admin keys</th>
                <th className="py-2 pr-3 font-medium">Managed</th>
                <th className="py-2 pr-3 font-medium">Last verified</th>
                <th className="py-2 pr-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n) => {
                const isPending = pending.has(n.nodeNum)
                const fb = feedback.get(n.nodeNum)
                return (
                  <tr
                    key={n.nodeNum}
                    className="border-b border-dark-700/30 hover:bg-dark-800/50"
                  >
                    <td className="py-2 pr-3">
                      <div className="text-xs text-dark-100">
                        <span className="font-semibold">
                          {n.longName || n.shortName || n.sensorShortId || `node ${n.nodeNum}`}
                        </span>
                        {n.shortName && n.longName && (
                          <span className="text-dark-400">
                            {' · '}
                            {n.shortName}
                          </span>
                        )}
                      </div>
                      <div className="text-[10px] text-dark-500 font-mono">
                        {n.nodeId || `!${n.nodeNum.toString(16)}`}
                      </div>
                    </td>
                    <td className="py-2 pr-3">
                      <TrustHealthPill
                        driftStatus={n.driftStatus}
                        lastVerifiedAt={n.lastVerifiedAt}
                        currentPskFp={n.currentPskFp}
                        fleetPrimaryFp={fleetPrimaryFp}
                      />
                    </td>
                    <td className="py-2 pr-3">
                      <div className="flex items-center gap-1 flex-wrap">
                        {n.adminKeyFingerprints.length === 0 && (
                          <span className="text-[10px] text-dark-500 italic">
                            empty
                          </span>
                        )}
                        {n.adminKeyFingerprints.map((fp) => {
                          const reg = labelByFp.get(fp)
                          return (
                            <PubkeyChip
                              key={fp}
                              fingerprint={fp}
                              label={reg?.label ?? '<unknown>'}
                              role={reg?.role ?? 'operator'}
                              compact
                            />
                          )
                        })}
                      </div>
                    </td>
                    <td className="py-2 pr-3">
                      {n.isManaged ? (
                        <span className="text-[10px] uppercase tracking-wider text-amber-300">
                          managed
                        </span>
                      ) : (
                        <span className="text-[10px] text-dark-500">no</span>
                      )}
                    </td>
                    <td className={`py-2 pr-3 text-[10px] ${lastVerifiedClass(n.lastVerifiedAt)}`}>
                      {n.lastVerifiedAt
                        ? new Date(n.lastVerifiedAt).toLocaleString()
                        : '—'}
                    </td>
                    <td className="py-2 pr-3 text-right">
                      <div className="inline-flex items-center gap-2 justify-end">
                        {fb?.kind === 'err' && (
                          <span
                            className="text-[10px] text-red-300 truncate max-w-[200px]"
                            title={fb.text}
                          >
                            {fb.text}
                          </span>
                        )}
                        {fb?.kind === 'ok' && (
                          <span className="text-[10px] text-emerald-300">✓ verified</span>
                        )}
                        <button
                          type="button"
                          onClick={() => runVerify(n.nodeNum)}
                          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-dark-700 hover:bg-dark-600 text-[10px] text-dark-200"
                        >
                          {isPending && (
                            <span
                              className="inline-block w-2.5 h-2.5 rounded-full border border-dark-300 border-t-transparent animate-spin"
                              aria-hidden
                            />
                          )}
                          {isPending ? 'Verifying…' : 'Verify'}
                        </button>
                        {isAdmin && (
                          <button
                            type="button"
                            onClick={() => setEditing(n)}
                            className="px-2 py-0.5 rounded bg-dark-700 hover:bg-dark-600 text-[10px] text-dark-200"
                          >
                            Edit
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {editing && (
        <EditAdminKeysModal
          node={editing}
          onClose={() => {
            setEditing(null)
            qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
          }}
        />
      )}
    </section>
  )
}
