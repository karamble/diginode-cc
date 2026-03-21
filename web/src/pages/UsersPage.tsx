import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface User {
  id: string
  email: string
  name?: string
  role: 'ADMIN' | 'OPERATOR' | 'ANALYST' | 'VIEWER'
  totpEnabled: boolean
  mustChangePassword: boolean
  tosAccepted: boolean
  locked?: boolean
  lastLogin?: string
  siteId?: string
  createdAt: string
}

const roleBadgeColors: Record<string, string> = {
  ADMIN: 'bg-purple-600/20 text-purple-400 border-purple-500/30',
  OPERATOR: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
  ANALYST: 'bg-green-600/20 text-green-400 border-green-500/30',
  VIEWER: 'bg-gray-600/20 text-gray-400 border-gray-500/30',
}

const roles = ['ADMIN', 'OPERATOR', 'ANALYST', 'VIEWER'] as const

export default function UsersPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [showInvite, setShowInvite] = useState(false)
  const [newUser, setNewUser] = useState({ email: '', password: '', name: '', role: 'VIEWER' as string })
  const [invite, setInvite] = useState({ email: '', role: 'VIEWER' as string })
  const [editingUser, setEditingUser] = useState<string | null>(null)
  const [editRole, setEditRole] = useState<string>('')
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null)

  const { data: users, isLoading, error } = useQuery<User[]>({
    queryKey: ['users'],
    queryFn: () => api.get('/users'),
  })

  const createMutation = useMutation({
    mutationFn: (body: { email: string; password: string; name: string; role: string }) =>
      api.post('/users', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      setShowCreate(false)
      setNewUser({ email: '', password: '', name: '', role: 'VIEWER' })
    },
  })

  const inviteMutation = useMutation({
    mutationFn: (body: { email: string; role: string }) =>
      api.post('/users/invite', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      setShowInvite(false)
      setInvite({ email: '', role: 'VIEWER' })
    },
  })

  const updateRoleMutation = useMutation({
    mutationFn: ({ id, role }: { id: string; role: string }) =>
      api.put(`/users/${id}`, { role }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      setEditingUser(null)
      setEditRole('')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      setDeleteConfirm(null)
    },
  })

  const unlockMutation = useMutation({
    mutationFn: (id: string) => api.post(`/users/${id}/unlock`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['users'] }),
  })

  const formatDate = (dateStr?: string) => {
    if (!dateStr) return 'Never'
    const d = new Date(dateStr)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  const startEditRole = (user: User) => {
    setEditingUser(user.id)
    setEditRole(user.role)
  }

  const saveRole = (id: string) => {
    updateRoleMutation.mutate({ id, role: editRole })
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Users</h2>
          <p className="text-sm text-dark-400 mt-1">
            {users ? `${users.length} user${users.length !== 1 ? 's' : ''} registered` : 'Loading...'}
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => { setShowInvite(!showInvite); setShowCreate(false) }}
            className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-200 text-sm rounded font-medium transition-colors border border-dark-600"
          >
            {showInvite ? 'Cancel' : 'Invite User'}
          </button>
          <button
            onClick={() => { setShowCreate(!showCreate); setShowInvite(false) }}
            className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
          >
            {showCreate ? 'Cancel' : 'Create User'}
          </button>
        </div>
      </div>

      {/* Create User Form */}
      {showCreate && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <h3 className="text-sm font-medium text-dark-200 mb-3">Create User</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
            <input
              type="email"
              placeholder="Email"
              value={newUser.email}
              onChange={(e) => setNewUser({ ...newUser, email: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <input
              type="text"
              placeholder="Name"
              value={newUser.name}
              onChange={(e) => setNewUser({ ...newUser, name: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <input
              type="password"
              placeholder="Password"
              value={newUser.password}
              onChange={(e) => setNewUser({ ...newUser, password: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <select
              value={newUser.role}
              onChange={(e) => setNewUser({ ...newUser, role: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            >
              {roles.map((r) => (
                <option key={r} value={r}>{r.charAt(0) + r.slice(1).toLowerCase()}</option>
              ))}
            </select>
            <button
              onClick={() => createMutation.mutate(newUser)}
              disabled={!newUser.email || !newUser.password || createMutation.isPending}
              className="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
            >
              {createMutation.isPending ? 'Creating...' : 'Create'}
            </button>
          </div>
          {createMutation.isError && (
            <p className="mt-2 text-sm text-red-400">{(createMutation.error as Error).message}</p>
          )}
        </div>
      )}

      {/* Invite User Form */}
      {showInvite && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <h3 className="text-sm font-medium text-dark-200 mb-3">Invite User</h3>
          <p className="text-xs text-dark-400 mb-3">Send an invitation email. The user will set their own password.</p>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <input
              type="email"
              placeholder="Email address"
              value={invite.email}
              onChange={(e) => setInvite({ ...invite, email: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <select
              value={invite.role}
              onChange={(e) => setInvite({ ...invite, role: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            >
              {roles.map((r) => (
                <option key={r} value={r}>{r.charAt(0) + r.slice(1).toLowerCase()}</option>
              ))}
            </select>
            <button
              onClick={() => inviteMutation.mutate(invite)}
              disabled={!invite.email || inviteMutation.isPending}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
            >
              {inviteMutation.isPending ? 'Sending...' : 'Send Invite'}
            </button>
          </div>
          {inviteMutation.isError && (
            <p className="mt-2 text-sm text-red-400">{(inviteMutation.error as Error).message}</p>
          )}
        </div>
      )}

      {/* Users Table */}
      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading users...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load users: {(error as Error).message}</p>
          </div>
        ) : !users || users.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">No users found</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">User</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Role</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">2FA</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Status</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Last Login</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Created</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {users.map((user: User) => (
                  <tr key={user.id} className="hover:bg-dark-800/30 transition-colors">
                    {/* User info (combined email + name) */}
                    <td className="px-4 py-3">
                      <div className="text-sm text-dark-200">{user.email}</div>
                      {user.name && <div className="text-xs text-dark-500">{user.name}</div>}
                    </td>

                    {/* Role (editable) */}
                    <td className="px-4 py-3">
                      {editingUser === user.id ? (
                        <div className="flex items-center gap-1">
                          <select
                            value={editRole}
                            onChange={(e) => setEditRole(e.target.value)}
                            className="px-2 py-1 bg-dark-800 border border-primary-500 rounded text-dark-100 text-xs focus:outline-none"
                          >
                            {roles.map((r) => (
                              <option key={r} value={r}>{r}</option>
                            ))}
                          </select>
                          <button
                            onClick={() => saveRole(user.id)}
                            disabled={updateRoleMutation.isPending}
                            className="px-2 py-1 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-xs rounded transition-colors"
                          >
                            {updateRoleMutation.isPending ? '...' : 'OK'}
                          </button>
                          <button
                            onClick={() => setEditingUser(null)}
                            className="px-2 py-1 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded transition-colors"
                          >
                            X
                          </button>
                        </div>
                      ) : (
                        <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${roleBadgeColors[user.role] || roleBadgeColors.VIEWER}`}>
                          {user.role}
                        </span>
                      )}
                    </td>

                    {/* 2FA status */}
                    <td className="px-4 py-3">
                      {user.totpEnabled ? (
                        <span className="inline-flex items-center gap-1 text-green-400 text-xs">
                          <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                            <path strokeLinecap="round" strokeLinejoin="round" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
                          </svg>
                          Enabled
                        </span>
                      ) : (
                        <span className="text-dark-500 text-xs">Off</span>
                      )}
                    </td>

                    {/* Status */}
                    <td className="px-4 py-3">
                      {user.locked ? (
                        <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded border bg-red-600/20 text-red-400 border-red-500/30">
                          Locked
                        </span>
                      ) : user.mustChangePassword ? (
                        <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded border bg-amber-600/20 text-amber-400 border-amber-500/30">
                          Pwd Reset
                        </span>
                      ) : !user.tosAccepted ? (
                        <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded border bg-amber-600/20 text-amber-400 border-amber-500/30">
                          No TOS
                        </span>
                      ) : (
                        <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded border bg-green-600/20 text-green-400 border-green-500/30">
                          Active
                        </span>
                      )}
                    </td>

                    {/* Last Login */}
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(user.lastLogin)}</td>

                    {/* Created */}
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(user.createdAt)}</td>

                    {/* Actions */}
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        {/* Edit role */}
                        {editingUser !== user.id && (
                          <button
                            onClick={() => startEditRole(user)}
                            className="text-xs text-dark-400 hover:text-primary-400 transition-colors"
                            title="Edit role"
                          >
                            Edit
                          </button>
                        )}

                        {/* Unlock (only shown for locked users) */}
                        {user.locked && (
                          <button
                            onClick={() => unlockMutation.mutate(user.id)}
                            disabled={unlockMutation.isPending}
                            className="text-xs text-amber-400 hover:text-amber-300 transition-colors"
                            title="Unlock user"
                          >
                            Unlock
                          </button>
                        )}

                        {/* Delete */}
                        {deleteConfirm === user.id ? (
                          <div className="flex items-center gap-1">
                            <span className="text-xs text-dark-400">Sure?</span>
                            <button
                              onClick={() => deleteMutation.mutate(user.id)}
                              disabled={deleteMutation.isPending}
                              className="text-xs text-red-400 hover:text-red-300 font-medium transition-colors"
                            >
                              {deleteMutation.isPending ? '...' : 'Yes'}
                            </button>
                            <button
                              onClick={() => setDeleteConfirm(null)}
                              className="text-xs text-dark-400 hover:text-dark-200 transition-colors"
                            >
                              No
                            </button>
                          </div>
                        ) : (
                          <button
                            onClick={() => setDeleteConfirm(user.id)}
                            className="text-xs text-red-400 hover:text-red-300 transition-colors"
                            title="Delete user"
                          >
                            Delete
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
      </div>

      {/* Mutation error display */}
      {(updateRoleMutation.isError || unlockMutation.isError) && (
        <p className="mt-4 text-sm text-red-400">
          {updateRoleMutation.isError && `Role update failed: ${(updateRoleMutation.error as Error).message}`}
          {unlockMutation.isError && `Unlock failed: ${(unlockMutation.error as Error).message}`}
        </p>
      )}
    </div>
  )
}
