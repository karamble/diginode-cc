package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/targets"
)

func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Targets.GetAll()
	if list == nil {
		list = []*targets.Target{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	var t targets.Target
	if err := readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if t.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := s.svc.Targets.Create(r.Context(), &t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create target")
		return
	}

	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleUpdateTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var t targets.Target
	if err := readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Targets.Update(r.Context(), id, &t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update target")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleClearTargets removes all targets from memory and the database.
func (s *Server) handleClearTargets(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Targets.ClearAll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear targets: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleResolveTarget marks a target as resolved/closed.
func (s *Server) handleResolveTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Targets.Resolve(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReactivateTarget flips a resolved target back to active.
func (s *Server) handleReactivateTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Targets.Reactivate(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reactivate target: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Targets.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete target")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetTargetPositions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []struct{}{})
}

// handleListTargetHits returns the per-hit history for a target, newest
// first. Powers the TargetsPage history modal — one row per Target:
// frame the firmware sent that resolved to this target_id (across MAC
// rotations for BLE fingerprint targets).
func (s *Server) handleListTargetHits(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	hits, err := s.svc.Targets.ListHits(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list target hits: "+err.Error())
		return
	}
	if hits == nil {
		hits = []targets.Hit{}
	}
	writeJSON(w, http.StatusOK, hits)
}

// createBLETargetRequest is the JSON shape posted by the BLEDetectionsPage
// dialog. detectionId is the optional ble_detections.id of the row the
// operator clicked — it's used only to resolve a default target name when
// the operator didn't override; the fingerprint fields in the body are
// authoritative regardless.
type createBLETargetRequest struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	DetectionID *int64                 `json:"detectionId,omitempty"`
	Fingerprint *targets.BLEFingerprint `json:"fingerprint"`
}

// handleCreateBLETarget creates a BLE fingerprint target from the
// per-field selection the operator made on a BLE Detections row. The
// dialog pre-fills the fingerprint from the row's classification result,
// then posts only the fields the operator opted to include.
func (s *Server) handleCreateBLETarget(w http.ResponseWriter, r *http.Request) {
	var req createBLETargetRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Fingerprint == nil {
		writeError(w, http.StatusBadRequest, "fingerprint required")
		return
	}
	if req.Name == "" {
		// Fall back to a generic name; the dialog should always supply one,
		// but the API tolerates empty so curl/test clients still work.
		req.Name = "BLE target"
	}

	t, err := s.svc.Targets.CreateBLETarget(r.Context(), req.Name, req.Description, req.Fingerprint, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "create BLE target: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}
