#!/usr/bin/env bash
#
# simulate-drone.sh — Simulate AntiHunter DigiNode sensor detections.
#
# Sends text lines to the DigiNode CC /api/serial/simulate endpoint,
# matching the exact message format that AntiHunter sensor firmware
# transmits over Meshtastic LoRa.
#
# Usage:
#   ./scripts/simulate-drone.sh                            # drones (default)
#   ./scripts/simulate-drone.sh --mode full                # all entity types
#   ./scripts/simulate-drone.sh --mode triangulation       # T_D/T_F/T_C sequence
#   ./scripts/simulate-drone.sh --mode attacks             # deauth events
#   ./scripts/simulate-drone.sh --mode targets             # WiFi/BLE devices
#   ./scripts/simulate-drone.sh --mode status              # node heartbeats
#   ./scripts/simulate-drone.sh --drone-count 3 --speed 40 # custom drones
#
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
BASE_URL="${BASE_URL:-http://localhost:3000}"
EMAIL="admin@example.com"
PASSWORD="admin"
MODE="drones"

# Coordinates (where the sensor node is)
TARGET_LAT=50.1481
TARGET_LON=19.0054

# Drone simulation
DRONE_COUNT=1
ITERATIONS=20
INTERVAL=3
START_DISTANCE=600
ALTITUDE=120.0
SPEED_KMH=40
NODE_ID="AH-SIM"
DRONE_ID=""
MAC=""
WITH_TARGETS=false
FAA_TEST=false

# ── Parse arguments ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)           MODE="$2"; shift 2 ;;
    --base-url)       BASE_URL="$2"; shift 2 ;;
    --lat)            TARGET_LAT="$2"; shift 2 ;;
    --lon)            TARGET_LON="$2"; shift 2 ;;
    --drone-count)    DRONE_COUNT="$2"; shift 2 ;;
    --iterations)     ITERATIONS="$2"; shift 2 ;;
    --interval)       INTERVAL="$2"; shift 2 ;;
    --distance)       START_DISTANCE="$2"; shift 2 ;;
    --altitude)       ALTITUDE="$2"; shift 2 ;;
    --speed)          SPEED_KMH="$2"; shift 2 ;;
    --node-id)        NODE_ID="$2"; shift 2 ;;
    --drone-id)       DRONE_ID="$2"; shift 2 ;;
    --mac)            MAC="$2"; shift 2 ;;
    --with-targets)   WITH_TARGETS=true; shift ;;
    --faa-test)       FAA_TEST=true; shift ;;
    --email)          EMAIL="$2"; shift 2 ;;
    --password)       PASSWORD="$2"; shift 2 ;;
    -h|--help)
      cat <<HELP
Usage: $0 [OPTIONS]

Simulate AntiHunter DigiNode sensor detections.

Modes (--mode):
  drones          Drone detection with zigzag flight paths (default)
  targets         WiFi/BLE device detections around the sensor
  triangulation   T_D → T_F → T_C sequence from multiple nodes
  attacks         Deauth/disassoc attack events
  status          Periodic sensor node STATUS heartbeats
  full            Run all modes sequentially

Options:
  --base-url URL    DigiNode CC URL (default: http://localhost:3000)
  --lat LAT         Sensor node latitude (default: 50.1481)
  --lon LON         Sensor node longitude (default: 19.0054)
  --drone-count N   Number of drones (default: 1)
  --iterations N    Updates per simulation (default: 20)
  --interval SECS   Seconds between updates (default: 3)
  --distance M      Drone start distance in meters (default: 600)
  --altitude M      Drone starting altitude (default: 120)
  --speed KMH       Drone approach speed km/h (default: 40)
  --node-id ID      Sensor node ID (default: AH-SIM)
  --drone-id ID     Override drone ID
  --mac MAC         Override drone MAC
  --with-targets    Also generate WiFi target detections during drone sim
  --email EMAIL     Login email (default: admin@example.com)
  --password PASS   Login password (default: admin)
HELP
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

API="${BASE_URL}/api"

# ── Authenticate ──────────────────────────────────────────────────────────────
echo "Authenticating to ${BASE_URL}..."
TOKEN=$(curl -sf "${API}/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\"}" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])" 2>/dev/null) || {
  echo "ERROR: Authentication failed. Is DigiNode CC running at ${BASE_URL}?"
  exit 1
}
echo "Authenticated."

# ── Helpers ───────────────────────────────────────────────────────────────────
send_lines() {
  curl -sf "${API}/serial/simulate" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"lines\": ${1}}" > /dev/null
}

random_mac() {
  printf '60:60:1F:%02X:%02X:%02X' $((RANDOM % 256)) $((RANDOM % 256)) $((RANDOM % 256))
}

random_target_mac() {
  # Locally administered bit set (AA:xx) for randomized devices
  printf 'AA:BB:CC:%02X:%02X:%02X' $((RANDOM % 256)) $((RANDOM % 256)) $((RANDOM % 256))
}

random_ble_mac() {
  printf 'DE:AD:%02X:%02X:%02X:%02X' $((RANDOM % 256)) $((RANDOM % 256)) $((RANDOM % 256)) $((RANDOM % 256))
}

random_drone_id() {
  local chars="0123456789ABCDEFGHJKLMNPQRSTUVWXYZ"
  local id="1581F5F"
  for _ in $(seq 1 12); do
    id+="${chars:$((RANDOM % ${#chars})):1}"
  done
  echo "$id"
}

offset_position() {
  python3 -c "
import math
lat, lon = $1, $2
d, h = $3, math.radians($4)
dlat = math.cos(h) * d / 111320.0
dlon = math.sin(h) * d / (111320.0 * math.cos(math.radians(lat)))
print(f'{lat + dlat:.6f} {lon + dlon:.6f}')
"
}

distance_m() {
  python3 -c "
import math
lat1, lon1, lat2, lon2 = $1, $2, $3, $4
dlat = (lat2 - lat1) * 111320
dlon = (lon2 - lon1) * 111320 * math.cos(math.radians((lat1 + lat2) / 2))
print(f'{math.sqrt(dlat**2 + dlon**2):.1f}')
"
}

jitter_pos() {
  # Add small random offset to a position (meters)
  python3 -c "
import random
lat, lon, meters = $1, $2, $3
dlat = random.gauss(0, meters / 111320)
dlon = random.gauss(0, meters / (111320 * __import__('math').cos(__import__('math').radians(lat))))
print(f'{lat + dlat:.6f} {lon + dlon:.6f}')
"
}

# ── Bootstrap sensor node ────────────────────────────────────────────────────
bootstrap_node() {
  local nid="$1" lat="$2" lon="$3"
  local line="${nid}: STATUS: Mode:WiFi+BLE Scan:ACTIVE Hits:0 Temp:38C Up:00:05:00 GPS:$(printf '%.6f' $lat),$(printf '%.6f' $lon)"
  send_lines "[\"${line}\"]"
  echo "  Node ${nid} at ${lat}, ${lon}"
}

# ══════════════════════════════════════════════════════════════════════════════
# MODE: drones — flying drones with zigzag approach
# ══════════════════════════════════════════════════════════════════════════════
run_drones() {
  echo ""
  echo "═══ DRONES: ${DRONE_COUNT} drone(s), ${ITERATIONS} updates, ${INTERVAL}s interval, ${SPEED_KMH} km/h"
  echo ""

  # Known FAA-resolvable drone IDs (from local faa_registry)
  local FAA_IDS=("FA2RGE1234" "1581F5FSIMTEST001" "3NZEH9K00D001F" "5YPJK2M00B002A")
  local FAA_MACS=("60:60:1F:D0:01:F0" "60:60:1F:B0:02:A0" "60:60:1F:AB:CD:EF" "60:60:1F:11:22:33")

  for d in $(seq 1 "$DRONE_COUNT"); do
    (
      if [[ -n "$DRONE_ID" && "$DRONE_COUNT" -eq 1 ]]; then
        did="$DRONE_ID"
      elif [[ -n "$DRONE_ID" ]]; then
        did="${DRONE_ID}-${d}"
      elif [[ $d -le ${#FAA_IDS[@]} ]]; then
        # Use known FAA IDs for first drones so FAA lookup works
        did="${FAA_IDS[$((d-1))]}"
      else
        did=$(random_drone_id)
      fi

      if [[ -n "$MAC" && "$DRONE_COUNT" -eq 1 ]]; then
        mac="$MAC"
      elif [[ $d -le ${#FAA_MACS[@]} && -z "$MAC" ]]; then
        mac="${FAA_MACS[$((d-1))]}"
      else
        mac=$(random_mac)
      fi

      heading=$(python3 -c "import random; print(f'{random.uniform(0, 360):.1f}')")
      start_pos=$(offset_position "$TARGET_LAT" "$TARGET_LON" "$START_DISTANCE" "$heading")
      drone_lat=$(echo "$start_pos" | awk '{print $1}')
      drone_lon=$(echo "$start_pos" | awk '{print $2}')
      op_offset=$(python3 -c "import random; print(f'{random.uniform(-0.002, 0.002):.6f} {random.uniform(-0.002, 0.002):.6f}')")
      op_lat=$(python3 -c "print(f'{$drone_lat + $(echo $op_offset | awk '{print $1}'):.6f}')")
      op_lon=$(python3 -c "print(f'{$drone_lon + $(echo $op_offset | awk '{print $2}'):.6f}')")
      alt="$ALTITUDE"
      spd=$(python3 -c "print(f'{$SPEED_KMH / 3.6:.1f}')")

      echo "[${did}] MAC=${mac} heading=${heading}deg"

      for i in $(seq 1 "$ITERATIONS"); do
        dist=$(distance_m "$drone_lat" "$drone_lon" "$TARGET_LAT" "$TARGET_LON")
        mps=$(python3 -c "print(f'{($SPEED_KMH * 1000.0 / 3600.0) * $INTERVAL:.1f}')")

        new_pos=$(python3 -c "
import math, random
lat1, lon1, lat2, lon2, step_i = $drone_lat, $drone_lon, $TARGET_LAT, $TARGET_LON, $i
dlat = (lat2 - lat1) * 111320
dlon = (lon2 - lon1) * 111320 * math.cos(math.radians((lat1 + lat2) / 2))
dist = math.sqrt(dlat**2 + dlon**2)
if dist < 30: print(f'{lat2:.6f} {lon2:.6f}')
else:
    step = min($mps, dist)
    fwd = step * 0.7 / dist
    bearing = math.atan2(dlon, dlat)
    amp = min(80, dist * 0.12)
    lateral = amp * math.sin(step_i * 1.3)
    perp = bearing + math.pi / 2
    nlat = lat1 + (lat2-lat1)*fwd + lateral*math.cos(perp)/111320 + random.gauss(0,0.000003)
    nlon = lon1 + (lon2-lon1)*fwd + lateral*math.sin(perp)/(111320*math.cos(math.radians(lat1))) + random.gauss(0,0.000003)
    print(f'{nlat:.6f} {nlon:.6f}')
")
        drone_lat=$(echo "$new_pos" | awk '{print $1}')
        drone_lon=$(echo "$new_pos" | awk '{print $2}')
        rssi=$(python3 -c "print(max(-95, min(-30, -30 - int($dist / 20))))")
        alt=$(python3 -c "import random; print(f'{$alt + random.uniform(-0.5, 0.5):.1f}')")

        LINE="${NODE_ID}: DRONE: ${mac} ID:${did} R${rssi} GPS:${drone_lat},${drone_lon} ALT:${alt} SPD:${spd} OP:${op_lat},${op_lon}"

        if [[ "$WITH_TARGETS" == "true" && $((i % 3)) -eq 1 ]]; then
          TMAC=$(random_target_mac)
          TRSSI=$(( -60 - RANDOM % 30 ))
          TLINE="${NODE_ID}: Target: WiFi ${TMAC} RSSI:${TRSSI} Name:DJI-RC2-SIM GPS:${drone_lat},${drone_lon}"
          send_lines "[\"${LINE}\", \"${TLINE}\"]"
          echo "  [${did}] #${i}/${ITERATIONS} dist=$(printf '%.0f' $dist)m rssi=${rssi} +target"
        else
          send_lines "[\"${LINE}\"]"
          echo "  [${did}] #${i}/${ITERATIONS} dist=$(printf '%.0f' $dist)m rssi=${rssi}"
        fi

        if python3 -c "import sys; sys.exit(0 if $dist < 30 else 1)" 2>/dev/null; then
          echo "  [${did}] Reached target!"; break
        fi
        sleep "$INTERVAL"
      done
      echo "[${did}] Done."
    ) &
    [[ "$DRONE_COUNT" -gt 1 ]] && sleep 1
  done
  wait
}

# ══════════════════════════════════════════════════════════════════════════════
# MODE: targets — WiFi and BLE device detections
# ══════════════════════════════════════════════════════════════════════════════
run_targets() {
  echo ""
  echo "═══ TARGETS: ${ITERATIONS} detections, ${INTERVAL}s interval"
  echo ""

  local WIFI_DEVICES=("DJI-RC2-SIM" "iPhone_John" "Galaxy-S24" "NETGEAR-5G" "HiddenCam")
  local BLE_NAMES=("AirTag-X" "Tile-Tracker" "FitBit-HR" "BLE-Beacon" "Unknown")
  local CHANNELS=(1 6 11 1 6 11 3 8 13)

  for i in $(seq 1 "$ITERATIONS"); do
    local lines="["

    # WiFi device
    local wmac=$(random_target_mac)
    local wrssi=$(( -40 - RANDOM % 50 ))
    local wch=${CHANNELS[$((RANDOM % ${#CHANNELS[@]}))]}
    local wname=${WIFI_DEVICES[$((RANDOM % ${#WIFI_DEVICES[@]}))]}
    local wpos=$(jitter_pos "$TARGET_LAT" "$TARGET_LON" 50)
    local wlat=$(echo "$wpos" | awk '{print $1}')
    local wlon=$(echo "$wpos" | awk '{print $2}')
    lines+="\"${NODE_ID}: Target: WiFi ${wmac} RSSI:${wrssi} Name:${wname} GPS:${wlat},${wlon}\""

    # BLE device (every other iteration)
    if [[ $((i % 2)) -eq 0 ]]; then
      local bmac=$(random_ble_mac)
      local brssi=$(( -50 - RANDOM % 40 ))
      local bname=${BLE_NAMES[$((RANDOM % ${#BLE_NAMES[@]}))]}
      lines+=",\"${NODE_ID}: Target: BLE ${bmac} RSSI:${brssi} Name:${bname}\""
    fi

    # DEVICE line (raw device scan, every 3rd)
    if [[ $((i % 3)) -eq 0 ]]; then
      local dmac=$(random_target_mac)
      local drssi=$(( -45 - RANDOM % 45 ))
      local dch=${CHANNELS[$((RANDOM % ${#CHANNELS[@]}))]}
      lines+=",\"${NODE_ID}: DEVICE:${dmac} W ${drssi} C${dch}\""
    fi

    lines+="]"
    send_lines "$lines"
    echo "  #${i}/${ITERATIONS} WiFi:${wmac} rssi=${wrssi} ch=${wch} name=${wname}"
    sleep "$INTERVAL"
  done
}

# ══════════════════════════════════════════════════════════════════════════════
# MODE: triangulation — T_D from multiple nodes → T_F → T_C
# ══════════════════════════════════════════════════════════════════════════════
run_triangulation() {
  echo ""
  echo "═══ TRIANGULATION: 3-node triangulation sequence"
  echo ""

  local TRI_MAC=$(random_target_mac)
  local NODE1="AH-N1"
  local NODE2="AH-N2"
  local NODE3="AH-N3"

  # Place 3 sensor nodes in a triangle around the target
  local n1=$(offset_position "$TARGET_LAT" "$TARGET_LON" 80 0)
  local n2=$(offset_position "$TARGET_LAT" "$TARGET_LON" 80 120)
  local n3=$(offset_position "$TARGET_LAT" "$TARGET_LON" 80 240)
  local n1_lat=$(echo "$n1" | awk '{print $1}') n1_lon=$(echo "$n1" | awk '{print $2}')
  local n2_lat=$(echo "$n2" | awk '{print $1}') n2_lon=$(echo "$n2" | awk '{print $2}')
  local n3_lat=$(echo "$n3" | awk '{print $1}') n3_lon=$(echo "$n3" | awk '{print $2}')

  # Bootstrap the 3 nodes
  bootstrap_node "$NODE1" "$n1_lat" "$n1_lon"
  bootstrap_node "$NODE2" "$n2_lat" "$n2_lon"
  bootstrap_node "$NODE3" "$n3_lat" "$n3_lon"
  sleep 1

  echo "  Target MAC: ${TRI_MAC}"
  echo "  Nodes: ${NODE1} ${NODE2} ${NODE3} in triangle (80m radius)"

  # Phase 1: T_D detections from each node (5 rounds)
  for round in $(seq 1 5); do
    local r1=$(( -40 - RANDOM % 20 ))
    local r2=$(( -45 - RANDOM % 20 ))
    local r3=$(( -50 - RANDOM % 20 ))
    send_lines "[\"${NODE1}: T_D: ${TRI_MAC} RSSI:${r1} Hits=${round} Type:WiFi GPS=${n1_lat},${n1_lon}\",\"${NODE2}: T_D: ${TRI_MAC} RSSI:${r2} Hits=${round} Type:WiFi GPS=${n2_lat},${n2_lon}\",\"${NODE3}: T_D: ${TRI_MAC} RSSI:${r3} Hits=${round} Type:BLE GPS=${n3_lat},${n3_lon}\"]"
    echo "  T_D round ${round}: N1=${r1}dBm N2=${r2}dBm N3=${r3}dBm"
    sleep "$INTERVAL"
  done

  # Phase 2: T_F final result
  local conf=$(python3 -c "import random; print(f'{random.uniform(72, 95):.1f}')")
  local unc=$(python3 -c "import random; print(f'{random.uniform(5, 25):.1f}')")
  local fix=$(jitter_pos "$TARGET_LAT" "$TARGET_LON" 15)
  local fix_lat=$(echo "$fix" | awk '{print $1}')
  local fix_lon=$(echo "$fix" | awk '{print $2}')

  send_lines "[\"${NODE1}: T_F: MAC=${TRI_MAC} GPS=${fix_lat},${fix_lon} CONF=${conf} UNC=${unc}\"]"
  echo "  T_F: GPS=${fix_lat},${fix_lon} CONF=${conf}% UNC=${unc}m"
  sleep 1

  # Phase 3: T_C complete
  send_lines "[\"${NODE1}: T_C: MAC=${TRI_MAC} Nodes=3\"]"
  echo "  T_C: Complete (3 nodes)"
}

# ══════════════════════════════════════════════════════════════════════════════
# MODE: attacks — Deauth/disassoc events
# ══════════════════════════════════════════════════════════════════════════════
run_attacks() {
  echo ""
  echo "═══ ATTACKS: ${ITERATIONS} attack events, ${INTERVAL}s interval"
  echo ""

  local ATTACK_TYPES=("DEAUTH" "DISASSOC")
  local CHANNELS=(1 6 11 6 1 11)

  for i in $(seq 1 "$ITERATIONS"); do
    local atype=${ATTACK_TYPES[$((RANDOM % 2))]}
    local src=$(random_target_mac)
    local dst="FF:FF:FF:FF:FF:FF"  # broadcast
    [[ $((RANDOM % 3)) -eq 0 ]] && dst=$(random_target_mac)  # sometimes targeted
    local arssi=$(( -30 - RANDOM % 40 ))
    local ach=${CHANNELS[$((RANDOM % ${#CHANNELS[@]}))]}

    local line="${NODE_ID}: ATTACK: ${atype} ${src}->${dst} R${arssi} C${ach}"
    send_lines "[\"${line}\"]"

    local mode="BROADCAST"
    [[ "$dst" != "FF:FF:FF:FF:FF:FF" ]] && mode="TARGETED"
    echo "  #${i}/${ITERATIONS} ${atype} [${mode}] src=${src} rssi=${arssi} ch=${ach}"
    sleep "$INTERVAL"
  done
}

# ══════════════════════════════════════════════════════════════════════════════
# MODE: status — Periodic sensor node heartbeats
# ══════════════════════════════════════════════════════════════════════════════
run_status() {
  echo ""
  echo "═══ STATUS: ${ITERATIONS} heartbeats, ${INTERVAL}s interval"
  echo ""

  local hits=0
  local uptime_s=300

  for i in $(seq 1 "$ITERATIONS"); do
    hits=$(( hits + RANDOM % 10 ))
    uptime_s=$(( uptime_s + INTERVAL ))
    local up_h=$(( uptime_s / 3600 ))
    local up_m=$(( (uptime_s % 3600) / 60 ))
    local up_s=$(( uptime_s % 60 ))
    local temp=$(python3 -c "import random; print(f'{35 + random.uniform(0, 15):.0f}')")
    local up=$(printf '%02d:%02d:%02d' $up_h $up_m $up_s)

    local line="${NODE_ID}: STATUS: Mode:WiFi+BLE Scan:ACTIVE Hits:${hits} Temp:${temp}C Up:${up} GPS:$(printf '%.6f' $TARGET_LAT),$(printf '%.6f' $TARGET_LON)"
    send_lines "[\"${line}\"]"
    echo "  #${i}/${ITERATIONS} hits=${hits} temp=${temp}C up=${up}"
    sleep "$INTERVAL"
  done
}

# ══════════════════════════════════════════════════════════════════════════════
# Main
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "Bootstrapping sensor node '${NODE_ID}'..."
bootstrap_node "$NODE_ID" "$TARGET_LAT" "$TARGET_LON"
sleep 1

case "$MODE" in
  drones)
    run_drones
    ;;
  targets)
    run_targets
    ;;
  triangulation)
    run_triangulation
    ;;
  attacks)
    run_attacks
    ;;
  status)
    run_status
    ;;
  full)
    echo ""
    echo "╔══════════════════════════════════════════╗"
    echo "║  FULL SIMULATION — all entity types      ║"
    echo "╚══════════════════════════════════════════╝"
    run_status &
    sleep 2
    run_drones &
    sleep 2
    run_targets &
    sleep 2
    run_attacks &
    sleep 5
    run_triangulation
    wait
    ;;
  *)
    echo "ERROR: Unknown mode '${MODE}'. Use: drones, targets, triangulation, attacks, status, full"
    exit 1
    ;;
esac

echo ""
echo "Simulation complete (mode: ${MODE})."
