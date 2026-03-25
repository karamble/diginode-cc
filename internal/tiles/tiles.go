package tiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

// Image magic bytes for validation
var (
	pngMagic  = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
)

func isValidPNG(data []byte) bool {
	return len(data) >= 8 && bytes.Equal(data[:8], pngMagic)
}

func isValidJPEG(data []byte) bool {
	return len(data) >= 3 && bytes.Equal(data[:3], jpegMagic)
}

// TileProvider describes an upstream tile source.
type TileProvider struct {
	Name        string
	URLTemplate string   // placeholders: {s}, {z}, {x}, {y}, {token}
	Subdomains  []string // nil if no subdomain rotation
	MaxZoom     int
	NeedsToken  bool
	ImageType   string // "png" or "jpeg"
	UserAgent   string
}

// preloadState tracks a running tile preload operation.
type preloadState struct {
	mu        sync.Mutex
	total     int
	completed int
	failed    int
	skipped   int
	active    bool
	cancel    context.CancelFunc
	startedAt time.Time
}

// TileCache manages cached map tiles with disk storage for offline access.
type TileCache struct {
	cacheDir  string
	jawgToken string
	client    *http.Client
	resolver  *net.Resolver
	semaphore chan struct{} // Rate limiter (max 2 concurrent fetches)
	online    atomic.Bool
	preload   preloadState
	providers map[string]*TileProvider
}

// newTileResolver creates a DNS resolver that dials public DNS directly,
// bypassing any broken system resolver (e.g. captive-portal dnsmasq).
func newTileResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			conn, err := d.DialContext(ctx, "udp", "8.8.8.8:53")
			if err != nil {
				conn, err = d.DialContext(ctx, "udp", "1.1.1.1:53")
			}
			return conn, err
		},
	}
}

func defaultProviders() map[string]*TileProvider {
	return map[string]*TileProvider{
		"jawg": {
			Name:        "jawg",
			URLTemplate: "https://tile.jawg.io/jawg-matrix/{z}/{x}/{y}.png?access-token={token}",
			MaxZoom:     22,
			NeedsToken:  true,
			ImageType:   "png",
			UserAgent:   "DigiNode-CC/1.0",
		},
		"osm": {
			Name:        "osm",
			URLTemplate: "https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png",
			Subdomains:  []string{"a", "b", "c"},
			MaxZoom:     19,
			ImageType:   "png",
			UserAgent:   "DigiNode-CC/1.0 (github.com/karamble/diginode-cc)",
		},
		"esri": {
			Name:        "esri",
			URLTemplate: "https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}",
			MaxZoom:     18,
			ImageType:   "jpeg",
			UserAgent:   "DigiNode-CC/1.0",
		},
	}
}

// NewTileCache creates a new tile cache with the specified directory.
func NewTileCache(cacheDir string, jawgToken string) *TileCache {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		log.Printf("Warning: Failed to create tile cache directory %s: %v", cacheDir, err)
	}

	resolver := newTileResolver()

	tc := &TileCache{
		cacheDir:  cacheDir,
		jawgToken: jawgToken,
		resolver:  resolver,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     30 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:  5 * time.Second,
					Resolver: resolver,
				}).DialContext,
			},
		},
		semaphore: make(chan struct{}, 2),
		providers: defaultProviders(),
	}

	tc.online.Store(tc.checkConnectivity())
	go tc.connectivityLoop()

	return tc
}

// checkConnectivity verifies we can resolve tile hostnames using our custom resolver.
func (tc *TileCache) checkConnectivity() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := tc.resolver.LookupHost(ctx, "tile.jawg.io")
	return err == nil && len(addrs) > 0
}

func (tc *TileCache) connectivityLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		tc.online.Store(tc.checkConnectivity())
	}
}

// buildUpstreamURL constructs the upstream tile URL from the provider template.
func (tc *TileCache) buildUpstreamURL(p *TileProvider, z, x, y int) string {
	url := p.URLTemplate
	if len(p.Subdomains) > 0 {
		s := p.Subdomains[(x+y)%len(p.Subdomains)]
		url = strings.ReplaceAll(url, "{s}", s)
	}
	url = strings.ReplaceAll(url, "{z}", strconv.Itoa(z))
	url = strings.ReplaceAll(url, "{x}", strconv.Itoa(x))
	url = strings.ReplaceAll(url, "{y}", strconv.Itoa(y))
	if p.NeedsToken {
		url = strings.ReplaceAll(url, "{token}", tc.jawgToken)
	}
	return url
}

// tileCachePath returns the filesystem path for a cached tile.
func (tc *TileCache) tileCachePath(provider string, z, x, y int) string {
	ext := "png"
	if p, ok := tc.providers[provider]; ok && p.ImageType == "jpeg" {
		ext = "jpg"
	}
	return filepath.Join(tc.cacheDir, provider, fmt.Sprintf("%d", z), fmt.Sprintf("%d", x), fmt.Sprintf("%d.%s", y, ext))
}

// isValidImage checks the image data against the expected format for the provider.
func (tc *TileCache) isValidImage(data []byte, provider string) bool {
	p, ok := tc.providers[provider]
	if !ok {
		return isValidPNG(data)
	}
	if p.ImageType == "jpeg" {
		return isValidJPEG(data)
	}
	return isValidPNG(data)
}

// contentType returns the MIME type for a provider's tiles.
func (tc *TileCache) contentType(provider string) string {
	p, ok := tc.providers[provider]
	if !ok {
		return "image/png"
	}
	if p.ImageType == "jpeg" {
		return "image/jpeg"
	}
	return "image/png"
}

// HandleTileRequest serves a map tile, either from cache or by fetching upstream.
func (tc *TileCache) HandleTileRequest(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	zStr := chi.URLParam(r, "z")
	xStr := chi.URLParam(r, "x")
	yStr := chi.URLParam(r, "y")

	provider, ok := tc.providers[providerName]
	if !ok {
		http.Error(w, "Unknown tile provider", http.StatusBadRequest)
		return
	}

	if provider.NeedsToken && tc.jawgToken == "" {
		http.Error(w, "Tile provider requires access token (JAWG_ACCESS_TOKEN not set)", http.StatusServiceUnavailable)
		return
	}

	z, err := strconv.Atoi(zStr)
	if err != nil || z < 0 || z > provider.MaxZoom {
		http.Error(w, "Invalid zoom level", http.StatusBadRequest)
		return
	}

	x, err := strconv.Atoi(xStr)
	if err != nil || x < 0 {
		http.Error(w, "Invalid X coordinate", http.StatusBadRequest)
		return
	}

	y, err := strconv.Atoi(yStr)
	if err != nil || y < 0 {
		http.Error(w, "Invalid Y coordinate", http.StatusBadRequest)
		return
	}

	maxTile := (1 << z) - 1
	if x > maxTile || y > maxTile {
		http.Error(w, "Coordinates out of bounds for zoom level", http.StatusBadRequest)
		return
	}

	cachePath := tc.tileCachePath(providerName, z, x, y)
	if tc.serveCachedTile(w, cachePath, providerName) {
		return
	}

	if !tc.online.Load() {
		tc.servePlaceholderTile(w)
		return
	}

	tc.fetchAndCacheTile(w, r, provider, z, x, y, cachePath, providerName)
}

// serveCachedTile attempts to serve a tile from disk cache.
func (tc *TileCache) serveCachedTile(w http.ResponseWriter, cachePath string, provider string) bool {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return false
	}

	if !tc.isValidImage(data, provider) {
		os.Remove(cachePath)
		return false
	}

	info, err := os.Stat(cachePath)
	if err != nil {
		return false
	}

	// If older than 30 days and we're online, let it be re-fetched
	if time.Since(info.ModTime()) > 30*24*time.Hour {
		if tc.online.Load() {
			return false
		}
	}

	w.Header().Set("Content-Type", tc.contentType(provider))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Tile-Cache", "HIT")
	w.Write(data)
	return true
}

// fetchAndCacheTile fetches a tile upstream, caches it, and serves it.
func (tc *TileCache) fetchAndCacheTile(w http.ResponseWriter, r *http.Request, provider *TileProvider, z, x, y int, cachePath string, providerName string) {
	// Acquire semaphore with timeout
	select {
	case tc.semaphore <- struct{}{}:
		defer func() { <-tc.semaphore }()
	case <-r.Context().Done():
		return
	case <-time.After(4 * time.Second):
		tc.servePlaceholderTile(w)
		return
	}

	upstreamURL := tc.buildUpstreamURL(provider, z, x, y)

	req, err := http.NewRequestWithContext(r.Context(), "GET", upstreamURL, nil)
	if err != nil {
		tc.servePlaceholderTile(w)
		return
	}
	req.Header.Set("User-Agent", provider.UserAgent)

	resp, err := tc.client.Do(req)
	if err != nil {
		log.Printf("Failed to fetch tile %s: %v", upstreamURL, err)
		tc.servePlaceholderTile(w)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Upstream returned status %d for tile %s", resp.StatusCode, upstreamURL)
		tc.servePlaceholderTile(w)
		return
	}

	tileData, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read tile data: %v", err)
		tc.servePlaceholderTile(w)
		return
	}

	if !tc.isValidImage(tileData, providerName) {
		log.Printf("Fetched tile %s is not a valid image, serving placeholder", upstreamURL)
		tc.servePlaceholderTile(w)
		return
	}

	go tc.cacheTileToDisk(cachePath, tileData)

	w.Header().Set("Content-Type", tc.contentType(providerName))
	w.Header().Set("Content-Length", strconv.Itoa(len(tileData)))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Tile-Cache", "MISS")
	w.Write(tileData)
}

// cacheTileToDisk saves a tile to the disk cache using atomic file writes.
func (tc *TileCache) cacheTileToDisk(cachePath string, data []byte) {
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("Failed to create tile cache directory %s: %v", dir, err)
		return
	}

	tmp, err := os.CreateTemp(dir, ".tile-*.tmp")
	if err != nil {
		log.Printf("Failed to create temp file for tile cache %s: %v", cachePath, err)
		return
	}
	tmpPath := tmp.Name()

	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		log.Printf("Failed to write tile to temp file %s: %v", tmpPath, err)
		return
	}
	if err := tmp.Close(); err != nil {
		log.Printf("Failed to close temp file %s: %v", tmpPath, err)
		return
	}

	if err := os.Rename(tmpPath, cachePath); err != nil {
		log.Printf("Failed to rename temp tile to cache %s: %v", cachePath, err)
		return
	}

	tmpPath = "" // Prevent deferred removal
}

// servePlaceholderTile serves a gray placeholder tile for offline mode.
func (tc *TileCache) servePlaceholderTile(w http.ResponseWriter) {
	placeholder := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00,
		0x08, 0x02, 0x00, 0x00, 0x00, 0xD3, 0x10, 0x3F,
		0x31,
		0x00, 0x00, 0x00, 0x1B, 0x49, 0x44, 0x41, 0x54,
		0x78, 0x9C, 0xED, 0xC1, 0x01, 0x0D, 0x00, 0x00,
		0x00, 0xC2, 0xA0, 0xF7, 0x4F, 0x6D, 0x0E, 0x37,
		0xA0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x7E, 0x0C, 0x47, 0x00, 0x01, 0x00, 0xA3,
		0x7F, 0xE0, 0x9E,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44,
		0xAE, 0x42, 0x60, 0x82,
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(placeholder)))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Tile-Cache", "PLACEHOLDER")
	w.Write(placeholder)
}

// ClearCache removes all cached tiles and recreates the cache directory.
func (tc *TileCache) ClearCache() error {
	if err := os.RemoveAll(tc.cacheDir); err != nil {
		return err
	}
	return os.MkdirAll(tc.cacheDir, 0755)
}

// --- Tile preloading ---

// PreloadRequest describes an area to preload tiles for.
type PreloadRequest struct {
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	RadiusKm float64 `json:"radius_km"`
	MinZoom  int     `json:"min_zoom"`
	MaxZoom  int     `json:"max_zoom"`
	Provider string  `json:"provider"`
}

func latLngToTile(lat, lng float64, zoom int) (int, int) {
	n := math.Pow(2, float64(zoom))
	x := int((lng + 180.0) / 360.0 * n)
	latRad := lat * math.Pi / 180.0
	y := int((1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) / 2.0 * n)
	maxTile := int(n) - 1
	if x < 0 {
		x = 0
	} else if x > maxTile {
		x = maxTile
	}
	if y < 0 {
		y = 0
	} else if y > maxTile {
		y = maxTile
	}
	return x, y
}

func tilesInRadius(lat, lng, radiusKm float64, minZoom, maxZoom int) [][3]int {
	dLat := radiusKm / 111.32
	dLng := radiusKm / (111.32 * math.Cos(lat*math.Pi/180.0))

	minLat := lat - dLat
	maxLat := lat + dLat
	minLng := lng - dLng
	maxLng := lng + dLng

	var tiles [][3]int
	for z := minZoom; z <= maxZoom; z++ {
		x1, y1 := latLngToTile(maxLat, minLng, z)
		x2, y2 := latLngToTile(minLat, maxLng, z)
		for x := x1; x <= x2; x++ {
			for y := y1; y <= y2; y++ {
				tiles = append(tiles, [3]int{z, x, y})
			}
		}
	}
	return tiles
}

// HandlePreloadStart starts a tile preload operation for an area.
func (tc *TileCache) HandlePreloadStart(w http.ResponseWriter, r *http.Request) {
	var req PreloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Default provider
	if req.Provider == "" {
		req.Provider = "jawg"
	}
	provider, ok := tc.providers[req.Provider]
	if !ok {
		http.Error(w, "Unknown tile provider", http.StatusBadRequest)
		return
	}

	if provider.NeedsToken && tc.jawgToken == "" {
		http.Error(w, "Tile provider requires access token (JAWG_ACCESS_TOKEN not set)", http.StatusServiceUnavailable)
		return
	}

	if req.RadiusKm <= 0 || req.RadiusKm > 50 {
		http.Error(w, "radius_km must be between 0 and 50", http.StatusBadRequest)
		return
	}
	if req.MinZoom < 0 || req.MinZoom > provider.MaxZoom {
		http.Error(w, fmt.Sprintf("min_zoom must be between 0 and %d", provider.MaxZoom), http.StatusBadRequest)
		return
	}
	if req.MaxZoom < req.MinZoom || req.MaxZoom > 16 {
		http.Error(w, "max_zoom must be between min_zoom and 16", http.StatusBadRequest)
		return
	}
	if req.Lat < -85.05 || req.Lat > 85.05 || req.Lng < -180 || req.Lng > 180 {
		http.Error(w, "Invalid lat/lng", http.StatusBadRequest)
		return
	}

	tc.preload.mu.Lock()
	if tc.preload.active {
		tc.preload.mu.Unlock()
		http.Error(w, "Preload already in progress", http.StatusConflict)
		return
	}

	tiles := tilesInRadius(req.Lat, req.Lng, req.RadiusKm, req.MinZoom, req.MaxZoom)
	if len(tiles) > 10000 {
		tc.preload.mu.Unlock()
		http.Error(w, fmt.Sprintf("Too many tiles (%d), max 10000. Reduce radius or zoom range.", len(tiles)), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	tc.preload.active = true
	tc.preload.total = len(tiles)
	tc.preload.completed = 0
	tc.preload.failed = 0
	tc.preload.skipped = 0
	tc.preload.cancel = cancel
	tc.preload.startedAt = time.Now()
	tc.preload.mu.Unlock()

	go tc.runPreload(ctx, tiles, req.Provider)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "started",
		"total":      len(tiles),
		"provider":   req.Provider,
		"radius_km":  req.RadiusKm,
		"zoom_range": fmt.Sprintf("%d-%d", req.MinZoom, req.MaxZoom),
	})
}

// HandlePreloadStatus returns the current preload progress.
func (tc *TileCache) HandlePreloadStatus(w http.ResponseWriter, r *http.Request) {
	tc.preload.mu.Lock()
	resp := map[string]any{
		"active":    tc.preload.active,
		"total":     tc.preload.total,
		"completed": tc.preload.completed,
		"failed":    tc.preload.failed,
		"skipped":   tc.preload.skipped,
	}
	if tc.preload.active {
		resp["elapsed_seconds"] = int(time.Since(tc.preload.startedAt).Seconds())
	}
	tc.preload.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandlePreloadCancel cancels a running preload operation.
func (tc *TileCache) HandlePreloadCancel(w http.ResponseWriter, r *http.Request) {
	tc.preload.mu.Lock()
	if !tc.preload.active {
		tc.preload.mu.Unlock()
		http.Error(w, "No preload in progress", http.StatusBadRequest)
		return
	}
	tc.preload.cancel()
	tc.preload.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelling"})
}

// runPreload downloads tiles in the background, skipping already-cached ones.
func (tc *TileCache) runPreload(ctx context.Context, tiles [][3]int, providerName string) {
	defer func() {
		tc.preload.mu.Lock()
		tc.preload.active = false
		tc.preload.cancel = nil
		tc.preload.mu.Unlock()
		log.Printf("Tile preload finished: %d completed, %d skipped, %d failed out of %d total",
			tc.preload.completed, tc.preload.skipped, tc.preload.failed, tc.preload.total)
	}()

	provider := tc.providers[providerName]

	for _, tile := range tiles {
		select {
		case <-ctx.Done():
			log.Println("Tile preload cancelled")
			return
		default:
		}

		z, x, y := tile[0], tile[1], tile[2]
		cachePath := tc.tileCachePath(providerName, z, x, y)

		// Skip if already cached and valid
		if data, err := os.ReadFile(cachePath); err == nil && tc.isValidImage(data, providerName) {
			tc.preload.mu.Lock()
			tc.preload.skipped++
			tc.preload.mu.Unlock()
			continue
		}

		// Acquire semaphore
		select {
		case tc.semaphore <- struct{}{}:
		case <-ctx.Done():
			return
		}

		upstreamURL := tc.buildUpstreamURL(provider, z, x, y)

		req, err := http.NewRequestWithContext(ctx, "GET", upstreamURL, nil)
		if err != nil {
			<-tc.semaphore
			tc.preload.mu.Lock()
			tc.preload.failed++
			tc.preload.mu.Unlock()
			continue
		}
		req.Header.Set("User-Agent", provider.UserAgent)

		resp, err := tc.client.Do(req)
		if err != nil {
			<-tc.semaphore
			tc.preload.mu.Lock()
			tc.preload.failed++
			tc.preload.mu.Unlock()
			continue
		}

		var tileData []byte
		if resp.StatusCode == http.StatusOK {
			tileData, err = io.ReadAll(resp.Body)
		}
		resp.Body.Close()
		<-tc.semaphore

		if err != nil || !tc.isValidImage(tileData, providerName) {
			tc.preload.mu.Lock()
			tc.preload.failed++
			tc.preload.mu.Unlock()
			continue
		}

		tc.cacheTileToDisk(cachePath, tileData)

		tc.preload.mu.Lock()
		tc.preload.completed++
		tc.preload.mu.Unlock()

		// Politeness delay
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
}
