// FleetSecurityPage is the /fleet-security route. Three stacked cards
// (Identity, Trust roster, Channels) plus a Recovery panel at the
// bottom. Channels and Recovery are placeholders pending steps 8 and 9.
//
// VIEWER role hits the route guard and gets redirected; OPERATOR sees
// read-only data; ADMIN gets the full set of actions on each card.

import { Navigate } from 'react-router-dom'
import { useState } from 'react'

import { useAuthStore } from '../stores/authStore'
import IdentityCard from './fleet/IdentityCard'
import TrustRoster from './fleet/TrustRoster'
import ChannelsCard from './fleet/ChannelsCard'
import RecoveryWizard from './fleet/RecoveryWizard'

export default function FleetSecurityPage() {
  const { user } = useAuthStore()
  const [recoveryOpen, setRecoveryOpen] = useState(false)
  const isAdmin = user?.role === 'ADMIN'

  // VIEWER (and unauthenticated, though Layout would have already
  // redirected) cannot see Fleet Security at all.
  if (!user || user.role === 'VIEWER') {
    return <Navigate to="/" replace />
  }

  return (
    <div className="p-6 space-y-5 max-w-6xl">
      <div>
        <h2 className="text-lg font-semibold text-dark-100">Fleet Security</h2>
        <p className="text-xs text-dark-400 mt-1">
          Control-center identity, per-node trust, and channel PSK lifecycle.
          See <code className="text-dark-300">FLEET_SECURITY.md</code> for the
          design overview.
        </p>
      </div>

      <IdentityCard />
      <TrustRoster />
      <ChannelsCard />

      {isAdmin && (
        <section className="bg-red-900/10 border border-red-700/30 rounded-lg p-5">
          <h3 className="text-sm font-semibold text-red-200">Recovery</h3>
          <p className="text-xs text-red-200/70 mt-1 mb-3">
            Use this if your control-center identity has been compromised
            or lost. Walks through installing a rescue keypair on the
            local Heltec, minting a new primary, pushing the new admin_key
            list to every reachable deployed node, then restoring the new
            primary locally. See FLEET_SECURITY.md §6.4 for the full
            playbook.
          </p>
          <button
            type="button"
            onClick={() => setRecoveryOpen(true)}
            className="px-3 py-1.5 rounded bg-red-700 hover:bg-red-600 text-xs text-white"
          >
            Recover from compromise…
          </button>
        </section>
      )}

      {recoveryOpen && (
        <RecoveryWizard onClose={() => setRecoveryOpen(false)} />
      )}
    </div>
  )
}
