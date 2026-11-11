package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zeta/portal/internal/auth"
)

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnvOwner(w, r, id) {
		return
	}
	var req struct {
		TTL   string `json:"ttl"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("JSON invalide"))
		return
	}
	ttl := defaultShareTTL
	if value := strings.TrimSpace(req.TTL); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("durée invalide: %v", err))
			return
		}
		ttl = parsed
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "Guest"
	}
	if len(label) > 80 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("le libellé doit faire 80 caractères maximum"))
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	share, token, err := s.shares.Create(id, label, sessionOwner(session), ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	share.URL = requestBaseURL(r) + "/s/" + token
	writeJSON(w, http.StatusCreated, share)
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnvOwner(w, r, id) {
		return
	}
	presence := s.sync.Presence(id)
	shares := s.shares.List(id)
	for i := range shares {
		shares[i].Connections = shareConnections(presence, shares[i].ID)
	}
	writeJSON(w, http.StatusOK, shares)
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnvOwner(w, r, id) {
		return
	}
	shareID := r.PathValue("shareID")
	hadIDE := false
	for _, participant := range s.sync.Presence(id) {
		if participant.ShareID == shareID && participant.Kind == "ide" {
			hadIDE = true
			break
		}
	}
	if !s.shares.Revoke(id, shareID) {
		writeError(w, http.StatusNotFound, fmt.Errorf("partage %s introuvable", shareID))
		return
	}
	if hadIDE {
		s.ide.Close(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) redeemShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	token := r.PathValue("token")
	share, ok := s.shares.Lookup(token)
	if !ok {
		s.clearShareCookie(w, r)
		http.Error(w, "lien de partage invalide ou expiré", http.StatusGone)
		return
	}
	s.setShareCookie(w, r, token, share.ExpiresAt)
	s.ensureEnvSession(r.Context(), share.EnvID)
	if err := s.builds.ActivateEnvironment(r.Context(), share.EnvID); err != nil {
		log.Printf("share activate %s: %v", share.EnvID, err)
		http.Error(w, "environnement indisponible", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/api/envs/"+url.PathEscape(share.EnvID)+"/ide/", http.StatusFound)
}
