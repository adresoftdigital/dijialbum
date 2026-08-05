package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type faceSearchRequest struct {
	AlbumID   string    `json:"album_id"`
	Embedding []float64 `json:"embedding"`
	Threshold float64   `json:"threshold"`
	Limit     int       `json:"limit"`
}

func (app *App) faceSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req faceSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON okunamadı"}`, http.StatusBadRequest)
		return
	}

	if req.AlbumID == "" || len(req.Embedding) != 128 {
		http.Error(w, `{"status":"error","message":"album_id ve 128 boyutlu embedding zorunlu"}`, http.StatusBadRequest)
		return
	}

	if req.Threshold <= 0 || req.Threshold > 2 {
		req.Threshold = 0.42
	}
	if req.Limit < 1 || req.Limit > 100 {
		req.Limit = 50
	}

	parts := make([]string, len(req.Embedding))
	for i, v := range req.Embedding {
		parts[i] = strconv.FormatFloat(v, 'f', 8, 64)
	}
	vecLiteral := "[" + strings.Join(parts, ",") + "]"

	rows, err := app.DB.Query(`
		SELECT media_id, url, COALESCE(thumbnail_url, ''), COALESCE(media_type, 'photo'), distance
		FROM match_faces($1::vector, $2::uuid, $3::float, $4::int)
	`, vecLiteral, req.AlbumID, req.Threshold, req.Limit)
	if err != nil {
		log.Printf("❌ faceSearch: %v", err)
		http.Error(w, `{"status":"error","message":"Arama hatası"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type matchItem struct {
		ID           string  `json:"id"`
		URL          string  `json:"url"`
		ThumbnailURL string  `json:"thumbnail_url"`
		MediaType    string  `json:"media_type"`
		IsVideo      bool    `json:"is_video"`
		Distance     float64 `json:"distance"`
	}

	var matches []matchItem
	for rows.Next() {
		var m matchItem
		var mediaType string

		err := rows.Scan(&m.ID, &m.URL, &m.ThumbnailURL, &mediaType, &m.Distance)
		if err != nil {
			log.Printf("⚠️ scan: %v", err)
			continue
		}

		m.MediaType = mediaType
		m.IsVideo = mediaType == "video"
		matches = append(matches, m)
	}

	if matches == nil {
		matches = []matchItem{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"count":   len(matches),
		"matches": matches,
	})
}

// Upload sırasında gelen embedding'leri kaydet
func (app *App) saveFaceEmbeddings(mediaID, albumID string, embeddings [][]float64) {
	for i, emb := range embeddings {
		if len(emb) != 128 {
			continue
		}
		parts := make([]string, 128)
		for j, v := range emb {
			parts[j] = strconv.FormatFloat(v, 'f', 8, 64)
		}
		vec := "[" + strings.Join(parts, ",") + "]"

		_, err := app.DB.Exec(`
			INSERT INTO public.face_embeddings (media_id, album_id, embedding, face_index)
			VALUES ($1, $2, $3::vector, $4)
		`, mediaID, albumID, vec, i)
		if err != nil {
			log.Printf("⚠️ embedding kayıt hatası: %v", err)
		}
	}
}
