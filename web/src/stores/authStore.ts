import { create } from 'zustand'
import api from '../api/client'

export type AuthStage = 'idle' | 'checking' | 'login' | 'legal' | 'twoFactor' | 'authenticated'

export interface User {
  id: string
  email: string
  name?: string
  role: string
  twoFactorEnabled?: boolean
  mustChangePassword?: boolean
}

interface AuthState {
  user: User | null
  token: string | null
  stage: AuthStage
  disclaimer: string | null
  error: string | null
  isAuthenticated: boolean
  setAuth: (user: User, token: string) => void
  setStage: (stage: AuthStage) => void
  setError: (err: string | null) => void
  setDisclaimer: (text: string | null) => void
  logout: () => void
  initialize: () => Promise<void>
}

export const useAuthStore = create<AuthState>((set, _get) => ({
  user: null,
  token: null,
  stage: 'idle',
  disclaimer: null,
  error: null,
  isAuthenticated: false,

  setAuth: (user, token) => {
    api.setToken(token)
    localStorage.setItem('cc_token', token)
    set({ user, token, stage: 'authenticated', isAuthenticated: true, error: null })
  },

  setStage: (stage) => set({ stage }),

  setError: (error) => set({ error }),

  setDisclaimer: (disclaimer) => set({ disclaimer }),

  logout: () => {
    api.setToken(null)
    localStorage.removeItem('cc_token')
    set({ user: null, token: null, stage: 'login', isAuthenticated: false, disclaimer: null, error: null })
  },

  initialize: async () => {
    const stored = localStorage.getItem('cc_token')
    if (!stored) {
      set({ stage: 'login' })
      return
    }
    set({ stage: 'checking' })
    try {
      api.setToken(stored)
      const user = await api.get<User>('/auth/me')
      set({ user, token: stored, stage: 'authenticated', isAuthenticated: true })
    } catch {
      localStorage.removeItem('cc_token')
      set({ stage: 'login' })
    }
  },
}))
