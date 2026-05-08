// TrustRoster renders the per-node trust table -- the heart of the
// Fleet Security tab. Each row shows last-seen, the admin_key list as
// labeled chips, is_managed status, and a trust-health pill. Per-row
// actions: Verify, Edit admin keys.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi, {
  type IdentityRecord,
  type NodeTrust,
} from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'
import PubkeyChip from '../../components/PubkeyChip'
import TrustHealthPill from '../../components/TrustHealthPill'
import EditAdminKeysModal from './EditAdminKeysModal'

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

  const labelByFp = new Map<string, IdentityRecord>()
  for (const r of registry ?? []) labelByFp.set(r.fingerprint, r)

  const verifyM = useMutation({
    mutationFn: (nodeNum: number) => fleetSecurityApi.verifyTrust(nodeNum),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
    },
  })

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
              {nodes.map((n) => (
                <tr
                  key={n.nodeNum}
                  className="border-b border-dark-700/30 hover:bg-dark-800/50"
                >
                  <td className="py-2 pr-3">
                    <div className="text-dark-100 text-xs">
                      {n.shortName || n.longName || n.sensorShortId || `node ${n.nodeNum}`}
                    </div>
                    <div className="text-[10px] text-dark-500 font-mono">
                      {n.nodeId || `!${n.nodeNum.toString(16)}`}
                    </div>
                  </td>
                  <td className="py-2 pr-3">
                    <TrustHealthPill
                      driftStatus={n.driftStatus}
                      lastVerifiedAt={n.lastVerifiedAt}
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
                  <td className="py-2 pr-3 text-[10px] text-dark-400">
                    {n.lastVerifiedAt
                      ? new Date(n.lastVerifiedAt).toLocaleString()
                      : '—'}
                  </td>
                  <td className="py-2 pr-3 text-right">
                    <div className="inline-flex items-center gap-1">
                      <button
                        type="button"
                        disabled={verifyM.isPending}
                        onClick={() => verifyM.mutate(n.nodeNum)}
                        className="px-2 py-0.5 rounded bg-dark-700 hover:bg-dark-600 text-[10px] text-dark-200"
                      >
                        Verify
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
              ))}
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
