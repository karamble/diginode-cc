// FleetSecurityPage is the /fleet-security route. Three stacked cards
// (Identity, Trust roster, Channels) plus a Recovery panel at the
// bottom. Channels and Recovery are placeholders pending steps 8 and 9.
//
// VIEWER role hits the route guard and gets redirected; OPERATOR sees
// read-only data; ADMIN gets the full set of actions on each card.

import { Navigate } from 'react-router-dom'

import { useAuthStore } from '../stores/authStore'
import IdentityCard from './fleet/IdentityCard'
import TrustRoster from './fleet/TrustRoster'

export default function FleetSecurityPage() {
  const { user } = useAuthStore()

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

      {/* Channels card lands in step 8 (PSK rotation). */}
      <section className="bg-dark-800/30 border border-dashed border-dark-700/50 rounded-lg p-5">
        <h3 className="text-sm font-semibold text-dark-300">Channels</h3>
        <p className="text-xs text-dark-500 mt-1">
          PSK rotation lands in a follow-up commit (FLEET_SECURITY.md §11
          step 8). This card will list each channel with PSK age, coverage
          gauge, and a Rotate PSK… action.
        </p>
      </section>

      {/* Recovery wizard lands in step 9. */}
      <section className="bg-dark-800/30 border border-dashed border-dark-700/50 rounded-lg p-5">
        <h3 className="text-sm font-semibold text-dark-300">Recovery</h3>
        <p className="text-xs text-dark-500 mt-1">
          The Recover from compromise… wizard lands in a follow-up commit
          (FLEET_SECURITY.md §6.4). Until then, operators recovering from
          an identity compromise must manually use a rescue key from the
          identity registry to push fresh admin_key lists via the Trust
          card's Edit action.
        </p>
      </section>
    </div>
  )
}
