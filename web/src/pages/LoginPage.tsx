import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '../stores/authStore'
import api from '../api/client'

export default function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const navigate = useNavigate()
  const { setAuth } = useAuthStore()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res: any = await api.post('/auth/login', { email, password })
      api.setToken(res.token)
      localStorage.setItem('cc_token', res.token)
      setAuth(res.user, res.token)
      navigate('/')
    } catch (err: any) {
      setError(err.message || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <img src="/logo111_dark.png" alt="DigiNode CC" className="h-16 mx-auto mb-4" />
        </div>
        <form onSubmit={handleSubmit} className="bg-surface rounded-xl border border-dark-700/50 p-6 space-y-4">
          {error && (
            <div className="bg-alert-critical/10 border border-alert-critical/30 rounded-lg p-3 text-sm text-alert-critical">
              {error}
            </div>
          )}
          <div>
            <label className="block text-xs font-medium text-dark-400 mb-1.5">Email</label>
            <input
              type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-dark-900 border border-dark-700 rounded-lg text-dark-100 text-sm focus:outline-none focus:border-primary-500 focus:ring-1 focus:ring-primary-500/30"
              placeholder="admin@example.com" required autoFocus
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-dark-400 mb-1.5">Password</label>
            <input
              type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-dark-900 border border-dark-700 rounded-lg text-dark-100 text-sm focus:outline-none focus:border-primary-500 focus:ring-1 focus:ring-primary-500/30"
              placeholder="Enter password" required
            />
          </div>
          <button
            type="submit" disabled={loading}
            className="w-full py-2.5 bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white text-sm font-medium rounded-lg transition-colors"
          >
            {loading ? 'Signing in...' : 'Sign In'}
          </button>
        </form>
      </div>
    </div>
  )
}
