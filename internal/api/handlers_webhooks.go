package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/karamble/diginode-cc/internal/webhooks"
)

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Webhooks.GetAll()
	if list == nil {
		list = []*webhooks.Webhook{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var wh webhooks.Webhook
	if err := readJSON(r, &wh); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Webhooks.Create(r.Context(), &wh); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create webhook")
		return
	}

	writeJSON(w, http.StatusCreated, wh)
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var wh webhooks.Webhook
	if err := readJSON(r, &wh); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.svc.Webhooks.Update(r.Context(), id, &wh); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update webhook")
		return
	}

	wh.ID = id
	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.svc.Webhooks.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete webhook")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	s.svc.Webhooks.Dispatch("webhook.test", map[string]interface{}{
		"webhookId": id,
		"message":   "test event",
		"timestamp": time.Now().UTC(),
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
