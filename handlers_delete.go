package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func (app *App) deleteMediaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"DELETE veya POST"}`, http.StatusMethodNotAllowed)
		return
	}

	mediaID := r.URL.Query().Get("media_id")
	if mediaID == "" {
		// JSON body alternatifi
		var body struct {
			MediaID string `json:"media_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			mediaID = body.MediaID
		}
	}
	if mediaID == "" {
		http.Error(w, `{"status":"error","message":"media_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	// Kayıt var mı + path için url al
	var albumID, url, thumbURL string
	err := app.DB.QueryRow(`
		SELECT album_id::text, url, COALESCE(thumbnail_url, '')
		FROM public.media
		WHERE id::text = $1
	`, mediaID).Scan(&albumID, &url, &thumbURL)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Medya bulunamadı"}`, http.StatusNotFound)
		return
	}

	// Yüz embedding'leri (CASCADE yoksa diye açık sil)
	if _, err := app.DB.Exec(`DELETE FROM public.face_embeddings WHERE media_id::text = $1`, mediaID); err != nil {
		log.Printf("⚠️ face_embeddings silinemedi: %v", err)
	}

	// media satırı
	if _, err := app.DB.Exec(`DELETE FROM public.media WHERE id::text = $1`, mediaID); err != nil {
		log.Printf("❌ media silinemedi: %v", err)
		http.Error(w, `{"status":"error","message":"Silme hatası"}`, http.StatusInternalServerError)
		return
	}

	// Storage (best-effort; DB silindi, dosya kalsa bile listede görünmez)
	go func() {
		_ = deleteFromSupabaseStorage(url)
		if thumbURL != "" {
			_ = deleteFromSupabaseStorage(thumbURL)
		}
	}()

	// Cache (varsa)
	if app.RDB != nil {
		// album detail cache'leri kısa ömürlü; zorunlu değil
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"media_id": mediaID,
		"album_id": albumID,
	})
}
