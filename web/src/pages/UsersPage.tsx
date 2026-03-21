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

export default function UsersPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [newUser, setNewUser] = useState({ email: '', password: '', name: '', role: 'VIEWER' as string })

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

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['users'] }),
  })

  const formatDate = (dateStr?: string) => {
    if (!dateStr) return 'Never'
    const d = new Date(dateStr)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-dark-100">Users</h2>
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
        >
          {showCreate ? 'Cancel' : 'Add User'}
        </button>
      </div>

      {showCreate && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <h3 className="text-sm font-medium text-dark-200 mb-3">Create User</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
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
            <div className="flex gap-2">
              <select
                value={newUser.role}
                onChange={(e) => setNewUser({ ...newUser, role: e.target.value })}
                className="flex-1 px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
              >
                <option value="ADMIN">Admin</option>
                <option value="OPERATOR">Operator</option>
                <option value="ANALYST">Analyst</option>
                <option value="VIEWER">Viewer</option>
              </select>
              <button
                onClick={() => createMutation.mutate(newUser)}
                disabled={!newUser.email || !newUser.password || createMutation.isPending}
                className="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
              >
                {createMutation.isPending ? 'Creating...' : 'Create'}
              </button>
            </div>
          </div>
          {createMutation.isError && (
            <p className="mt-2 text-sm text-red-400">{(createMutation.error as Error).message}</p>
          )}
        </div>
      )}

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
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Email</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Name</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Role</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">2FA</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Last Login</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Created</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {users.map((user: User) => (
                  <tr key={user.id} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3 text-sm text-dark-200">{user.email}</td>
                    <td className="px-4 py-3 text-sm text-dark-300">{user.name || '-'}</td>
                    <td className="px-4 py-3">
                      <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${roleBadgeColors[user.role] || roleBadgeColors.VIEWER}`}>
                        {user.role}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      {user.totpEnabled ? (
                        <span className="text-green-400 text-xs">Enabled</span>
                      ) : (
                        <span className="text-dark-500 text-xs">Off</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(user.lastLogin)}</td>
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(user.createdAt)}</td>
                    <td className="px-4 py-3 text-right">
                      <button
                        onClick={() => {
                          if (confirm(`Delete user ${user.email}?`)) {
                            deleteMutation.mutate(user.id)
                          }
                        }}
                        className="text-xs text-red-400 hover:text-red-300 transition-colors"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
