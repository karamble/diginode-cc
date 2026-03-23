#!/usr/bin/env bash
#
# simulate-drone.sh — Simulate an AntiHunter DigiNode sensor detecting a drone.
#
# Sends DRONE: telemetry lines to the local DigiNode CC /api/serial/simulate
# endpoint, matching the exact message format that AntiHunter sensor firmware
# transmits over Meshtastic LoRa. The full pipeline processes the data:
#   DigiNode CC text parser → DroneService → DB + WebSocket → gotailme dashboard
#
# Usage:
#   ./scripts/simulate-drone.sh                          # defaults
#   ./scripts/simulate-drone.sh --lat 50.148 --lon 19.005  # custom coordinates
#   ./scripts/simulate-drone.sh --drone-count 3 --iterations 30
#
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
BASE_URL="${BASE_URL:-http://localhost:3000}"
EMAIL="admin@example.com"
PASSWORD="admin"

# Target/node coordinates (where the gotailme device is)
TARGET_LAT=50.1481
TARGET_LON=19.0054

# Drone simulation
DRONE_COUNT=1
ITERATIONS=40
INTERVAL=5          # seconds between updates
START_DISTANCE=1200 # meters from target
ALTITUDE=120.0
SPEED_KMH=55        # approach speed km/h
NODE_ID="AH-SIM"
DRONE_ID=""
MAC=""

# ── Parse arguments ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
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
    --email)          EMAIL="$2"; shift 2 ;;
    --password)       PASSWORD="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: $0 [OPTIONS]"
      echo ""
      echo "Simulate drone detection from an AntiHunter sensor node."
      echo ""
      echo "Options:"
      echo "  --base-url URL    DigiNode CC URL (default: http://localhost:3000)"
      echo "  --lat LAT         Target latitude (default: 50.1481)"
      echo "  --lon LON         Target longitude (default: 19.0054)"
      echo "  --drone-count N   Number of drones (default: 1)"
      echo "  --iterations N    Telemetry updates per drone (default: 40)"
      echo "  --interval SECS   Seconds between updates (default: 5)"
      echo "  --distance M      Start distance in meters (default: 1200)"
      echo "  --altitude M      Starting altitude in meters (default: 120)"
      echo "  --speed KMH       Approach speed km/h (default: 55)"
      echo "  --node-id ID      Sensor node ID (default: AH-SIM)"
      echo "  --drone-id ID     Override drone ID"
      echo "  --mac MAC         Override drone MAC"
      echo "  --email EMAIL     Login email (default: admin@example.com)"
      echo "  --password PASS   Login password (default: admin)"
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

# ── Helper: send lines to simulate endpoint ───────────────────────────────────
send_lines() {
  local json_lines="$1"
  curl -sf "${API}/serial/simulate" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"lines\": ${json_lines}}" > /dev/null
}

# ── Helper: generate random MAC ──────────────────────────────────────────────
random_mac() {
  printf '60:60:1F:%02X:%02X:%02X' $((RANDOM % 256)) $((RANDOM % 256)) $((RANDOM % 256))
}

# ── Helper: generate DJI-style drone ID ──────────────────────────────────────
random_drone_id() {
  local chars="0123456789ABCDEFGHJKLMNPQRSTUVWXYZ"
  local id="1581F5F"
  for _ in $(seq 1 12); do
    id+="${chars:$((RANDOM % ${#chars})):1}"
  done
  echo "$id"
}

# ── Helper: offset lat/lon by meters and heading ─────────────────────────────
# Uses simple equirectangular approximation (good enough for <50km)
offset_position() {
  local lat="$1" lon="$2" dist_m="$3" heading_deg="$4"
  python3 -c "
import math
lat, lon = $lat, $lon
d, h = $dist_m, math.radians($heading_deg)
dlat = math.cos(h) * d / 111320.0
dlon = math.sin(h) * d / (111320.0 * math.cos(math.radians(lat)))
print(f'{lat + dlat:.6f} {lon + dlon:.6f}')
"
}

# ── Helper: compute distance between two points ──────────────────────────────
distance_m() {
  local lat1="$1" lon1="$2" lat2="$3" lon2="$4"
  python3 -c "
import math
lat1, lon1, lat2, lon2 = $lat1, $lon1, $lat2, $lon2
dlat = (lat2 - lat1) * 111320
dlon = (lon2 - lon1) * 111320 * math.cos(math.radians((lat1 + lat2) / 2))
print(f'{math.sqrt(dlat**2 + dlon**2):.1f}')
"
}

# ── Bootstrap: send STATUS line to register the simulated sensor node ─────────
echo ""
echo "Bootstrapping sensor node '${NODE_ID}' at ${TARGET_LAT}, ${TARGET_LON}..."
STATUS_LINE="${NODE_ID}: STATUS: Mode:WiFi+BLE Scan:ACTIVE Hits:0 Temp:38C Up:00:05:00 GPS:$(printf '%.6f' $TARGET_LAT),$(printf '%.6f' $TARGET_LON)"
send_lines "[\"${STATUS_LINE}\"]"
echo "  -> ${STATUS_LINE}"
sleep 1

# ── Simulate drones ──────────────────────────────────────────────────────────
echo ""
echo "Simulating ${DRONE_COUNT} drone(s): ${ITERATIONS} updates, ${INTERVAL}s interval, ${SPEED_KMH} km/h approach"
echo "Target: ${TARGET_LAT}, ${TARGET_LON} | Start distance: ${START_DISTANCE}m"
echo ""

for d in $(seq 1 "$DRONE_COUNT"); do
  (
    # Generate or use provided drone identity
    if [[ -n "$DRONE_ID" && "$DRONE_COUNT" -eq 1 ]]; then
      did="$DRONE_ID"
    elif [[ -n "$DRONE_ID" ]]; then
      did="${DRONE_ID}-${d}"
    else
      did=$(random_drone_id)
    fi

    if [[ -n "$MAC" && "$DRONE_COUNT" -eq 1 ]]; then
      mac="$MAC"
    else
      mac=$(random_mac)
    fi

    # Random starting heading (direction drone comes from)
    heading=$(python3 -c "import random; print(f'{random.uniform(0, 360):.1f}')")

    # Place drone at start distance
    start_pos=$(offset_position "$TARGET_LAT" "$TARGET_LON" "$START_DISTANCE" "$heading")
    drone_lat=$(echo "$start_pos" | awk '{print $1}')
    drone_lon=$(echo "$start_pos" | awk '{print $2}')

    # Operator near drone start
    op_offset=$(python3 -c "import random; print(f'{random.uniform(-0.002, 0.002):.6f} {random.uniform(-0.002, 0.002):.6f}')")
    op_lat=$(python3 -c "print(f'{$drone_lat + $(echo $op_offset | awk '{print $1}'):.6f}')")
    op_lon=$(python3 -c "print(f'{$drone_lon + $(echo $op_offset | awk '{print $2}'):.6f}')")

    alt="$ALTITUDE"
    spd=$(python3 -c "print(f'{$SPEED_KMH / 3.6:.1f}')") # km/h -> m/s
    rssi=-85

    echo "[${did}] MAC=${mac} heading=${heading}deg from ${drone_lat},${drone_lon}"

    for i in $(seq 1 "$ITERATIONS"); do
      # Move drone toward target
      dist=$(distance_m "$drone_lat" "$drone_lon" "$TARGET_LAT" "$TARGET_LON")
      meters_per_step=$(python3 -c "print(f'{($SPEED_KMH * 1000.0 / 3600.0) * $INTERVAL:.1f}')")

      # Bearing from drone to target
      new_pos=$(python3 -c "
import math
lat1, lon1 = $drone_lat, $drone_lon
lat2, lon2 = $TARGET_LAT, $TARGET_LON
dlat = (lat2 - lat1) * 111320
dlon = (lon2 - lon1) * 111320 * math.cos(math.radians((lat1 + lat2) / 2))
dist = math.sqrt(dlat**2 + dlon**2)
if dist < 20:
    print(f'{lat2:.6f} {lon2:.6f}')
else:
    step = min($meters_per_step, dist)
    ratio = step / dist
    # Add slight jitter for realism
    import random
    jlat = random.gauss(0, 0.000005)
    jlon = random.gauss(0, 0.000005)
    nlat = lat1 + (lat2 - lat1) * ratio + jlat
    nlon = lon1 + (lon2 - lon1) * ratio + jlon
    print(f'{nlat:.6f} {nlon:.6f}')
")
      drone_lat=$(echo "$new_pos" | awk '{print $1}')
      drone_lon=$(echo "$new_pos" | awk '{print $2}')

      # RSSI increases as drone approaches (less negative = stronger)
      rssi=$(python3 -c "
dist = $dist
rssi = max(-95, min(-30, -30 - int(dist / 20)))
print(rssi)
")
      # Vary altitude slightly
      alt=$(python3 -c "import random; print(f'{$alt + random.uniform(-0.5, 0.5):.1f}')")

      LINE="${NODE_ID}: DRONE: ${mac} ID:${did} R${rssi} GPS:${drone_lat},${drone_lon} ALT:${alt} SPD:${spd} OP:${op_lat},${op_lon}"
      send_lines "[\"${LINE}\"]"
      echo "  [${did}] #${i}/${ITERATIONS} dist=$(printf '%.0f' $dist)m rssi=${rssi} ${drone_lat},${drone_lon}"

      # Check if reached target
      if python3 -c "import sys; sys.exit(0 if $dist < 30 else 1)" 2>/dev/null; then
        echo "  [${did}] Reached target after ${i} steps!"
        break
      fi

      sleep "$INTERVAL"
    done

    echo "[${did}] Simulation complete."
  ) &

  # Stagger drone launches
  if [[ "$DRONE_COUNT" -gt 1 ]]; then
    sleep 1
  fi
done

# Wait for all background drone processes
wait
echo ""
echo "All drone simulations complete."
