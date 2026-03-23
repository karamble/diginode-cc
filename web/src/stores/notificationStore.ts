import { create } from 'zustand'

export interface AppNotification {
  id: string
  type: string        // 'geofence' | 'chat' | 'alert' | 'drone' | 'system'
  severity: string    // 'info' | 'notice' | 'alert' | 'critical'
  title: string
  message: string
  timestamp: string
  read: boolean
}

interface NotificationState {
  notifications: AppNotification[]
  unreadCount: number
  addNotification: (n: Omit<AppNotification, 'id' | 'read'>) => void
  markRead: (id: string) => void
  markAllRead: () => void
  dismiss: (id: string) => void
  clearAll: () => void
}

const MAX_NOTIFICATIONS = 50

export const useNotificationStore = create<NotificationState>((set) => ({
  notifications: [],
  unreadCount: 0,

  addNotification: (n) =>
    set((s) => {
      const notification: AppNotification = {
        ...n,
        id: crypto.randomUUID(),
        read: false,
      }
      const updated = [notification, ...s.notifications].slice(0, MAX_NOTIFICATIONS)
      return {
        notifications: updated,
        unreadCount: s.unreadCount + 1,
      }
    }),

  markRead: (id) =>
    set((s) => {
      let delta = 0
      const notifications = s.notifications.map((n) => {
        if (n.id === id && !n.read) {
          delta = 1
          return { ...n, read: true }
        }
        return n
      })
      return {
        notifications,
        unreadCount: Math.max(0, s.unreadCount - delta),
      }
    }),

  markAllRead: () =>
    set((s) => ({
      notifications: s.notifications.map((n) => ({ ...n, read: true })),
      unreadCount: 0,
    })),

  dismiss: (id) =>
    set((s) => {
      const target = s.notifications.find((n) => n.id === id)
      const wasUnread = target && !target.read ? 1 : 0
      return {
        notifications: s.notifications.filter((n) => n.id !== id),
        unreadCount: Math.max(0, s.unreadCount - wasUnread),
      }
    }),

  clearAll: () => set({ notifications: [], unreadCount: 0 }),
}))
