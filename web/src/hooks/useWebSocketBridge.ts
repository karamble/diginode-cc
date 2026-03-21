import { useEffect } from 'react'
import wsClient from '../api/websocket'
import { useDronesStore, type Drone } from '../stores/dronesStore'
import { useNodesStore, type MeshNode } from '../stores/nodesStore'
import { useGeofenceStore } from '../stores/geofenceStore'
import { useAlertStore } from '../stores/alertStore'
import { useChatStore } from '../stores/chatStore'
import { useTargetStore } from '../stores/targetStore'
import { useTerminalStore } from '../stores/terminalStore'
import type { AlertEvent, ChatMessage, Geofence, Target } from '../types/api'

/**
 * Centralized WebSocket event bridge.
 * Routes incoming WS events to the appropriate Zustand stores.
 * Mount once in the authenticated section of App.tsx.
 */
export function useWebSocketBridge() {
  useEffect(() => {
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
        useDronesStore.getState().updateDrone(p as Partial<Drone> & { id: string })
        useTerminalStore.getState().addEntry('drone.telemetry', p)
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
        useAlertStore.getState().addEvent(p as AlertEvent)
        useTerminalStore.getState().addEntry('alert', p)
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
      },

      // Geofence
      'geofence.event': (p) => {
        useTerminalStore.getState().addEntry('geofence.event', p)
      },

      // Target
      'target.update': (p) => {
        useTargetStore.getState().updateTarget(p as Target)
        useTerminalStore.getState().addEntry('target.update', p)
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
