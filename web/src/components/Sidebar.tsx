import { NavLink } from 'react-router-dom'

const navItems = [
  { path: '/map', label: 'Map', icon: '{}' },
  { path: '/nodes', label: 'Nodes', icon: '{}' },
  { path: '/drones', label: 'Drones', icon: '{}' },
  { path: '/alerts', label: 'Alerts', icon: '{}' },
  { path: '/chat', label: 'Chat', icon: '{}' },
  { path: '/targets', label: 'Targets', icon: '{}' },
  { path: '/geofences', label: 'Geofences', icon: '{}' },
  { path: '/inventory', label: 'Inventory', icon: '{}' },
  { path: '/commands', label: 'Commands', icon: '{}' },
  { path: '/webhooks', label: 'Webhooks', icon: '{}' },
  { path: '/adsb', label: 'ADS-B', icon: '{}' },
  { path: '/exports', label: 'Exports', icon: '{}' },
  { path: '/users', label: 'Users', icon: '{}' },
  { path: '/config', label: 'Config', icon: '{}' },
]

export default function Sidebar() {
  return (
    <aside className="w-56 bg-dark-900 border-r border-dark-700 flex flex-col">
      <div className="p-4 border-b border-dark-700">
        <h1 className="text-lg font-bold text-primary-400">DigiNode CC</h1>
        <p className="text-xs text-dark-400">Command Center</p>
      </div>
      <nav className="flex-1 overflow-y-auto py-2">
        {navItems.map((item) => (
          <NavLink
            key={item.path}
            to={item.path}
            className={({ isActive }) =>
              `flex items-center px-4 py-2 text-sm transition-colors ${
                isActive
                  ? 'bg-primary-900/30 text-primary-400 border-r-2 border-primary-400'
                  : 'text-dark-300 hover:bg-dark-800 hover:text-dark-100'
              }`
            }
          >
            <span>{item.label}</span>
          </NavLink>
        ))}
      </nav>
      <div className="p-3 border-t border-dark-700 text-xs text-dark-500">
        v0.1.0-dev
      </div>
    </aside>
  )
}
