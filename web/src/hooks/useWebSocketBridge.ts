import { useEffect, useRef } from 'react'
import wsClient from '../api/websocket'
import { useDronesStore, type Drone } from '../stores/dronesStore'
import { useNodesStore, type MeshNode } from '../stores/nodesStore'
import { useGeofenceStore } from '../stores/geofenceStore'
import { useAlertStore } from '../stores/alertStore'
import { useChatStore } from '../stores/chatStore'
import { useTargetStore } from '../stores/targetStore'
import { useTerminalStore } from '../stores/terminalStore'
import { useNotificationStore } from '../stores/notificationStore'
import type { AlertEvent, ChatMessage, Geofence, Target } from '../types/api'

/**
 * Centralized WebSocket event bridge.
 * Routes incoming WS events to the appropriate Zustand stores.
 * Mount once in the authenticated section of App.tsx.
 */
export function useWebSocketBridge() {
  const seenDronesRef = useRef(new Set<string>())

  useEffect(() => {
    const notify = useNotificationStore.getState().addNotification
    const seenDrones = seenDronesRef.current

    const handlers: Record<string, (payload: unknown) => void> = {
      // Initial state snapshot
      'init': (payload) => {
        const p = payload as Record<string, unknown>
        if (p.nodes) useNodesStore.getState().setNodes(p.nodes as MeshNode[])
        if (p.drones) useDronesStore.getState().setDrones(p.drones as Drone[])
        if (p.geofences) useGeofenceStore.getState().setGeofences(p.geofences as Geofence[])
        if (p.targets) useTargetStore.getState().setTargets(p.targets as Target[])
        if (p.alerts) useAlertStore.getState().setEvents(p.alerts as AlertEvent[])
        useTerminalStore.getState().addEntry('init', payload)
      },

      // Drone events
      'drone.telemetry': (p) => {
        const drone = p as Partial<Drone> & { id: string }
        useDronesStore.getState().updateDrone(drone)
        useTerminalStore.getState().addEntry('drone.telemetry', p)

        // Notify only on first sighting of a drone
        if (drone.id && !seenDrones.has(drone.id)) {
          seenDrones.add(drone.id)
          const label = (drone as Record<string, unknown>).uasId || (drone as Record<string, unknown>).mac || drone.id
          notify({
            type: 'drone',
            severity: 'alert',
            title: 'New drone detected',
            message: `Drone ${label}`,
            timestamp: new Date().toISOString(),
          })
        }
      },
      'drone.status': (p) => {
        useDronesStore.getState().updateDrone(p as Partial<Drone> & { id: string })
        useTerminalStore.getState().addEntry('drone.status', p)
      },
      'drone.remove': (p) => {
        const data = p as Record<string, string>
        useDronesStore.getState().removeDrone(data.id || data.droneId)
        useTerminalStore.getState().addEntry('drone.remove', p)
      },

      // Node events
      'node.update': (p) => {
        useNodesStore.getState().updateNode(p as Partial<MeshNode> & { id: string })
        useTerminalStore.getState().addEntry('node.update', p)
      },
      'node.remove': (p) => {
        const data = p as Record<string, string>
        useNodesStore.getState().removeNode(data.nodeId || data.id)
        useTerminalStore.getState().addEntry('node.remove', p)
      },
      'node.position': (p) => {
        useNodesStore.getState().updateNode(p as Partial<MeshNode> & { id: string })
        useTerminalStore.getState().addEntry('node.position', p)
      },

      // Alert
      'alert': (p) => {
        const evt = p as AlertEvent
        useAlertStore.getState().addEvent(evt)
        useTerminalStore.getState().addEntry('alert', p)
        // Skip notification for geofence-sourced alerts (already notified via geofence.event)
        const isGeofence = evt.title?.startsWith('Geofence breach:') || evt.data?.geofenceId
        if (!isGeofence) {
          notify({
            type: 'alert',
            severity: (evt.severity || 'alert').toLowerCase(),
            title: evt.title || 'Alert',
            message: evt.message || '',
            timestamp: evt.createdAt || new Date().toISOString(),
          })
        }
      },

      // Chat
      'chat.message': (p) => {
        const msg = p as ChatMessage
        useChatStore.getState().addMessage(msg)
        useTerminalStore.getState().addEntry('chat.message', p)

        // Track unread DMs: if this is a DM and we're not viewing that conversation
        const BROADCAST = 0xFFFFFFFF
        const isDM = msg.toNode !== 0 && msg.toNode !== BROADCAST
        if (isDM) {
          const peerNodeNum = msg.fromNode === 0 ? msg.toNode : msg.fromNode
          const { activeChat } = useChatStore.getState()
          if (activeChat.mode !== 'dm' || activeChat.peerNodeNum !== peerNodeNum) {
            useChatStore.getState().incrementUnread(peerNodeNum)
          }
        }

        // Notify for remote messages (not sent by us)
        if (msg.fromNode !== 0) {
          const nodeHex = `!${msg.fromNode.toString(16).padStart(8, '0')}`
          const preview = msg.text.length > 60 ? msg.text.slice(0, 57) + '...' : msg.text
          notify({
            type: 'chat',
            severity: 'info',
            title: isDM ? `DM from ${nodeHex}` : `Chat from ${nodeHex}`,
            message: preview,
            timestamp: msg.timestamp || new Date().toISOString(),
          })
        }
      },

      // Geofence
      'geofence.event': (p) => {
        useTerminalStore.getState().addEntry('geofence.event', p)
        const data = p as Record<string, unknown>
        const geofenceName = (data.geofence as Record<string, unknown>)?.name || data.geofenceName || 'Unknown'
        const level = ((data.alarmLevel as string) || 'alert').toLowerCase()
        notify({
          type: 'geofence',
          severity: level,
          title: `Geofence: ${geofenceName}`,
          message: (data.message as string) || `${data.entityType}/${data.entityId} entered geofence`,
          timestamp: new Date().toISOString(),
        })
      },

      // Target
      'target.update': (p) => {
        const target = p as Target
        useTargetStore.getState().updateTarget(target)
        useTerminalStore.getState().addEntry('target.update', p)

        // Notify when triangulation result arrives (has confidence data)
        if (target.trackingConfidence != null && target.trackingConfidence > 0) {
          const pct = Math.round((target.trackingConfidence as number) * 100)
          const unc = target.trackingUncertainty != null ? `±${Math.round(target.trackingUncertainty as number)}m` : ''
          notify({
            type: 'system',
            severity: pct >= 70 ? 'notice' : pct >= 50 ? 'alert' : 'critical',
            title: 'Triangulation complete',
            message: `${target.name || target.mac}: ${pct}% confidence ${unc}`,
            timestamp: new Date().toISOString(),
          })
        }
      },

      // Command
      'command.update': (p) => {
        useTerminalStore.getState().addEntry('command.update', p)
      },

      // Health
      'health': (p) => {
        useTerminalStore.getState().addEntry('health', p)
      },

      // Config
      'config.update': (p) => {
        useTerminalStore.getState().addEntry('config.update', p)
      },

      // Inventory
      'inventory.update': (p) => {
        useTerminalStore.getState().addEntry('inventory.update', p)
        const dev = p as Record<string, unknown>
        if (dev.hits === 1) {
          const label = (dev.deviceName as string) || (dev.lastSsid as string) || (dev.mac as string) || 'unknown'
          notify({
            type: 'system',
            severity: 'info',
            title: 'New device detected',
            message: `${label} (${(dev.deviceType as string) || 'unknown'})`,
            timestamp: new Date().toISOString(),
          })
        }
      },

      // ADS-B
      'adsb.update': (p) => {
        useTerminalStore.getState().addEntry('adsb.update', p)
      },

      // ACARS
      'acars.message': (p) => {
        useTerminalStore.getState().addEntry('acars.message', p)
      },
    }

    Object.entries(handlers).forEach(([event, handler]) => {
      wsClient.on(event, handler)
    })

    return () => {
      Object.entries(handlers).forEach(([event, handler]) => {
        wsClient.off(event, handler)
      })
    }
  }, [])
}
