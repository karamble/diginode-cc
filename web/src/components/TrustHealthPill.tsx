// TrustHealthPill renders the per-node trust-roster health as a colored
// chip. The pill answers a single question: does this node's admin_key
// list still match fleet policy, and did the last mesh round-trip
// succeed? Age of last verification lives in the "Last verified" column
// instead.
//
// Priority: drift > unreachable > unverified > old-psk > verified.
// A node that's known-drifted always pills as drift even if recently
// verified; a node mid-migration takes the amber "old psk" pill so the
// operator sees which fleet members still need Phase B/C to push them
// forward.

import type { DriftStatus } from '../api/fleetSecurity'

interface TrustHealthPillProps {
  driftStatus: DriftStatus
  lastVerifiedAt?: string
  // currentPskFp + fleetPrimaryFp drive the migration-lagging
  // detection. Both optional -- when either is missing (no rotation
  // ever ran, or the node is unverified post-000028) the pill falls
  // back to verified/unverified.
  currentPskFp?: string
  fleetPrimaryFp?: string
}

interface PillStyle {
  cls: string
  label: string
  title: string
}

function pillFor(
  driftStatus: DriftStatus,
  lastVerifiedAt?: string,
  currentPskFp?: string,
  fleetPrimaryFp?: string,
): PillStyle {
  if (driftStatus === 'drift') {
    return {
      cls: 'bg-red-600/20 text-red-300 border-red-600/40',
      label: 'drift',
      title: 'Node admin_key list does not match fleet policy',
    }
  }
  if (driftStatus === 'unreachable') {
    return {
      cls: 'bg-red-600/20 text-red-300 border-red-600/40',
      label: 'unreachable',
      title: 'Last verify attempt failed -- node may be offline or out of range',
    }
  }
  if (!lastVerifiedAt) {
    return {
      cls: 'bg-dark-700/40 text-dark-400 border-dark-600/40',
      label: 'unverified',
      title: 'Trust state has never been verified for this node',
    }
  }

  // Migration lagging: the node was last verified on a different PSK
  // than the channel's current PRIMARY. Operator action is Retry on
  // this node, so it takes precedence over the plain "verified" pill.
  if (currentPskFp && fleetPrimaryFp && currentPskFp !== fleetPrimaryFp) {
    return {
      cls: 'bg-amber-600/20 text-amber-300 border-amber-600/40',
      label: 'old psk',
      title: `Last verified on PSK ${currentPskFp.slice(0, 14)}… while fleet PRIMARY is ${fleetPrimaryFp.slice(0, 14)}… -- run Retry on this node to push the new PSK`,
    }
  }

  return {
    cls: 'bg-emerald-600/20 text-emerald-300 border-emerald-600/40',
    label: 'verified',
    title: 'admin_key list matches fleet policy and last verify succeeded',
  }
}

export default function TrustHealthPill({
  driftStatus,
  lastVerifiedAt,
  currentPskFp,
  fleetPrimaryFp,
}: TrustHealthPillProps) {
  const { cls, label, title } = pillFor(driftStatus, lastVerifiedAt, currentPskFp, fleetPrimaryFp)
  return (
    <span
      title={title}
      className={`inline-flex items-center px-2 py-0.5 rounded border text-[10px] font-medium uppercase tracking-wider ${cls}`}
    >
      {label}
    </span>
  )
}
