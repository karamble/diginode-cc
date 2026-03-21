import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '../stores/authStore'
import api from '../api/client'

interface LoginResponse {
  token: string
  user: { id: string; email: string; name?: string; role: string; twoFactorEnabled?: boolean; mustChangePassword?: boolean }
  twoFactorRequired?: boolean
  twoFactorToken?: string
  legalAccepted?: boolean
  disclaimer?: string
}

interface TwoFactorResponse {
  token: string
  user: { id: string; email: string; name?: string; role: string; twoFactorEnabled?: boolean; mustChangePassword?: boolean }
  legalAccepted?: boolean
  disclaimer?: string
}

interface LegalAckResponse {
  token: string
  user: { id: string; email: string; name?: string; role: string; twoFactorEnabled?: boolean; mustChangePassword?: boolean }
}

export default function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const navigate = useNavigate()
  const { setAuth, stage, setStage, disclaimer, setDisclaimer } = useAuthStore()

  // Temporary tokens used during multi-stage auth
  const [pendingToken, setPendingToken] = useState<string | null>(null)
  const [pendingUser, setPendingUser] = useState<LoginResponse['user'] | null>(null)

  const completeAuth = (user: LoginResponse['user'], token: string) => {
    setAuth(user, token)
    navigate('/')
  }

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await api.post<LoginResponse>('/auth/login', { email, password })

      // Stage 1: Check if 2FA is required
      if (res.twoFactorRequired) {
        setPendingToken(res.twoFactorToken || res.token)
        setPendingUser(res.user)
        setStage('twoFactor')
        setLoading(false)
        return
      }

      // Stage 2: Check if legal disclaimer needs acceptance
      if (res.legalAccepted === false) {
        setPendingToken(res.token)
        setPendingUser(res.user)
        setDisclaimer(res.disclaimer || 'You must accept the terms of service and legal disclaimer to continue.')
        setStage('legal')
        setLoading(false)
        return
      }

      // Fully authenticated
      completeAuth(res.user, res.token)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Login failed'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  const handleTwoFactor = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      api.setToken(pendingToken)
      const res = await api.post<TwoFactorResponse>('/auth/2fa/verify', { code: totpCode })

      // After 2FA, still need to check legal
      if (res.legalAccepted === false) {
        setPendingToken(res.token)
        setPendingUser(res.user)
        setDisclaimer(res.disclaimer || 'You must accept the terms of service and legal disclaimer to continue.')
        setStage('legal')
        setLoading(false)
        return
      }

      completeAuth(res.user, res.token)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '2FA verification failed'
      setError(msg)
      setTotpCode('')
    } finally {
      setLoading(false)
    }
  }

  const handleLegalAck = async () => {
    setError('')
    setLoading(true)
    try {
      api.setToken(pendingToken)
      const res = await api.post<LegalAckResponse>('/auth/legal-ack', {})
      completeAuth(res.user || pendingUser!, res.token || pendingToken!)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to accept terms'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  const handleBack = () => {
    setStage('login')
    setPendingToken(null)
    setPendingUser(null)
    setDisclaimer(null)
    setError('')
    setTotpCode('')
  }

  // --- Two-Factor Stage ---
  if (stage === 'twoFactor') {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <div className="w-full max-w-sm">
          <div className="text-center mb-8">
            <img src="/logo111_dark.png" alt="DigiNode CC" className="h-16 mx-auto mb-4" />
          </div>
          <form onSubmit={handleTwoFactor} className="bg-surface rounded-xl border border-dark-700/50 p-6 space-y-4">
            <div className="text-center">
              <h3 className="text-sm font-medium text-dark-200 mb-1">Two-Factor Authentication</h3>
              <p className="text-xs text-dark-400">Enter the 6-digit code from your authenticator app</p>
            </div>
            {error && (
              <div className="bg-alert-critical/10 border border-alert-critical/30 rounded-lg p-3 text-sm text-alert-critical">
                {error}
              </div>
            )}
            <div>
              <input
                type="text"
                value={totpCode}
                onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                className="w-full px-3 py-3 bg-dark-900 border border-dark-700 rounded-lg text-dark-100 text-center text-2xl tracking-[0.5em] font-mono focus:outline-none focus:border-primary-500 focus:ring-1 focus:ring-primary-500/30"
                placeholder="000000"
                maxLength={6}
                required
                autoFocus
              />
            </div>
            <button
              type="submit"
              disabled={loading || totpCode.length !== 6}
              className="w-full py-2.5 bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white text-sm font-medium rounded-lg transition-colors"
            >
              {loading ? 'Verifying...' : 'Verify'}
            </button>
            <button
              type="button"
              onClick={handleBack}
              className="w-full py-2 text-dark-400 hover:text-dark-200 text-sm transition-colors"
            >
              Back to login
            </button>
          </form>
        </div>
      </div>
    )
  }

  // --- Legal Disclaimer Stage ---
  if (stage === 'legal') {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <div className="w-full max-w-lg">
          <div className="text-center mb-8">
            <img src="/logo111_dark.png" alt="DigiNode CC" className="h-16 mx-auto mb-4" />
          </div>
          <div className="bg-surface rounded-xl border border-dark-700/50 p-6 space-y-4">
            <div className="text-center">
              <h3 className="text-sm font-medium text-dark-200 mb-1">Legal Disclaimer</h3>
              <p className="text-xs text-dark-400">Please review and accept to continue</p>
            </div>
            {error && (
              <div className="bg-alert-critical/10 border border-alert-critical/30 rounded-lg p-3 text-sm text-alert-critical">
                {error}
              </div>
            )}
            <div className="bg-dark-900 border border-dark-700 rounded-lg p-4 max-h-64 overflow-y-auto">
              <p className="text-sm text-dark-300 whitespace-pre-wrap leading-relaxed">
                {disclaimer || 'Loading disclaimer...'}
              </p>
            </div>
            <button
              onClick={handleLegalAck}
              disabled={loading}
              className="w-full py-2.5 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm font-medium rounded-lg transition-colors"
            >
              {loading ? 'Accepting...' : 'I Accept'}
            </button>
            <button
              type="button"
              onClick={handleBack}
              className="w-full py-2 text-dark-400 hover:text-dark-200 text-sm transition-colors"
            >
              Back to login
            </button>
          </div>
        </div>
      </div>
    )
  }

  // --- Login Stage (default) ---
  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <img src="/logo111_dark.png" alt="DigiNode CC" className="h-16 mx-auto mb-4" />
        </div>
        <form onSubmit={handleLogin} className="bg-surface rounded-xl border border-dark-700/50 p-6 space-y-4">
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
