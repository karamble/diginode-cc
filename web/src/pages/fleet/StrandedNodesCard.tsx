// StrandedNodesCard surfaces the post-rotation stranded-node recovery
// flow. Backend invariants:
//   - Phase C marks every non-migrated remote with stranded_since=now
//     and previous_psk_fp = the PSK that just got retired
//   - Pi keeps that PSK alive as a SECONDARY recovery slot (slots 2-7,
//     6 generations FIFO eviction)
//   - The dispatcher hook auto-fires recover_stranded the moment a
//     stranded node sends any packet on the matching channel hash
//
// This card lists who's stranded, how long, how many recovery attempts
// have been made, and offers per-node "Recover now" (force-enqueue
// outside the dispatcher's wait-for-traffic path) + "Stop trying"
// (clear the markers, give up — operator must USB-recover).
//
// Hidden when the list is empty: there's nothing useful to show, and
// surfacing an empty card adds visual noise to the common "all
// healthy" state.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import fleetSecurityApi, { type NodeTrust } from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'

function formatRelative(iso?: string): string {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return '—'
  const sec = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  const days = Math.floor(hr / 24)
  return `${days}d ago`
}

export default function StrandedNodesCard() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'
  const qc = useQueryClient()

  const { data: stranded, isLoading, error } = useQuery<NodeTrust[]>({
    queryKey: ['fleet-security', 'stranded'],
    queryFn: () => fleetSecurityApi.listStranded(),
    refetchInterval: 30000,
  })

  const recoverM = useMutation({
    mutationFn: (nodeNum: number) => fleetSecurityApi.recoverStrandedNow(nodeNum),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['fleet-security', 'stranded'] })
      qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
    },
  })

  const cancelM = useMutation({
    mutationFn: (nodeNum: number) => fleetSecurityApi.cancelStranded(nodeNum),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['fleet-security', 'stranded'] })
      qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
    },
  })

  // Hide entirely when nothing is stranded — the common "fleet healthy"
  // state shouldn't carry visual noise.
  if (isLoading) return null
  if (error) {
    return (
      <section className="bg-dark-800/60 border border-dark-700/50 rounded-lg p-5">
        <h3 className="text-sm font-semibold text-dark-100 mb-2">Stranded nodes</h3>
        <div className="text-xs text-red-300">{(error as Error).message}</div>
      </section>
    )
  }
  if (!stranded || stranded.length === 0) return null

  return (
    <section className="bg-amber-900/10 border border-amber-700/40 rounded-lg p-5">
      <header className="mb-4">
        <h3 className="text-sm font-semibold text-amber-100">
          Stranded nodes ({stranded.length})
        </h3>
        <p className="text-xs text-amber-200/70 mt-1">
          These nodes did not migrate during the most recent PSK
          rotation. The control center keeps the previous PSK alive on
          a recovery channel slot and auto-rotates each one as soon as
          it broadcasts. Use Recover now to skip the wait.
        </p>
      </header>

      <table className="w-full text-xs">
        <thead className="text-dark-400">
          <tr className="text-left">
            <th className="py-1 pr-3">Node</th>
            <th className="py-1 pr-3">Stranded since</th>
            <th className="py-1 pr-3">Last seen</th>
            <th className="py-1 pr-3">Attempts</th>
            <th className="py-1 pr-3">Last attempt</th>
            <th className="py-1 pr-3">Last error</th>
            {isAdmin && <th className="py-1 pr-3 text-right">Actions</th>}
          </tr>
        </thead>
        <tbody className="divide-y divide-dark-700/40">
          {stranded.map((n) => {
            const recovering =
              recoverM.isPending && recoverM.variables === n.nodeNum
            const cancelling =
              cancelM.isPending && cancelM.variables === n.nodeNum
            return (
              <tr key={n.nodeNum} className="text-dark-200">
                <td className="py-2 pr-3">
                  <div className="font-medium text-dark-100">
                    {n.shortName || n.longName || n.nodeId || `!${n.nodeNum.toString(16)}`}
                  </div>
                  <div className="text-dark-500 font-mono text-[10px]">
                    {n.nodeId || `!${n.nodeNum.toString(16).padStart(8, '0')}`}
                  </div>
                </td>
                <td className="py-2 pr-3 text-amber-200">
                  {formatRelative(n.strandedSince)}
                </td>
                <td className="py-2 pr-3">
                  <span className={n.isOnline ? 'text-emerald-300' : 'text-dark-400'}>
                    {formatRelative(n.lastHeard)}
                  </span>
                </td>
                <td className="py-2 pr-3">{n.recoveryAttempts ?? 0}</td>
                <td className="py-2 pr-3">{formatRelative(n.lastRecoveryAt)}</td>
                <td className="py-2 pr-3 text-dark-400">
                  {n.lastRecoveryError ? (
                    <span title={n.lastRecoveryError} className="font-mono text-[10px]">
                      {n.lastRecoveryError.length > 36
                        ? n.lastRecoveryError.slice(0, 36) + '…'
                        : n.lastRecoveryError}
                    </span>
                  ) : (
                    '—'
                  )}
                </td>
                {isAdmin && (
                  <td className="py-2 pr-3 text-right space-x-2 whitespace-nowrap">
                    <button
                      type="button"
                      disabled={recovering || cancelling}
                      onClick={() => recoverM.mutate(n.nodeNum)}
                      className="px-2 py-1 rounded bg-emerald-700/40 hover:bg-emerald-700/60 disabled:opacity-50 text-[11px] text-emerald-100"
                      title="Force-enqueue a recover_stranded job for this node, jumping the dispatcher's wait-for-inbound-traffic gate."
                    >
                      {recovering ? 'Queuing…' : 'Recover now'}
                    </button>
                    <button
                      type="button"
                      disabled={recovering || cancelling}
                      onClick={() => {
                        if (
                          confirm(
                            `Stop trying to recover ${n.shortName || n.nodeId || n.nodeNum}? The previous PSK pointer is wiped — recovery becomes impossible until the node is USB-recovered.`,
                          )
                        ) {
                          cancelM.mutate(n.nodeNum)
                        }
                      }}
                      className="px-2 py-1 rounded bg-dark-700/60 hover:bg-red-700/40 disabled:opacity-50 text-[11px] text-dark-200"
                      title="Clear the stranded marker and previous_psk_fp pointer. Operator gives up; node will need USB recovery if it ever returns."
                    >
                      {cancelling ? 'Cancelling…' : 'Stop trying'}
                    </button>
                  </td>
                )}
              </tr>
            )
          })}
        </tbody>
      </table>
    </section>
  )
}
