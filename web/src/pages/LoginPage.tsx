import { useState } from 'react'

export default function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    // TODO: Implement login
    console.log('login', email, password)
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-dark-950">
      <div className="w-full max-w-sm p-8 bg-dark-900 rounded-lg border border-dark-700">
        <h1 className="text-2xl font-bold text-center text-primary-400 mb-8">
          DigiNode CC
        </h1>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-dark-300 mb-1">Email</label>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 focus:outline-none focus:border-primary-500"
              required
            />
          </div>
          <div>
            <label className="block text-sm text-dark-300 mb-1">Password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 focus:outline-none focus:border-primary-500"
              required
            />
          </div>
          <button
            type="submit"
            className="w-full py-2 bg-primary-600 hover:bg-primary-700 text-white rounded font-medium transition-colors"
          >
            Sign In
          </button>
        </form>
      </div>
    </div>
  )
}
