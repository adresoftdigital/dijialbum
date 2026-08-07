package main

import (
	"context"
	"encoding/json"
	"fmt"
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

	var albumID, fileURL, thumbURL string
	err := app.DB.QueryRow(`
		SELECT album_id::text, url, COALESCE(thumbnail_url, '')
		FROM public.media
		WHERE id::text = $1
	`, mediaID).Scan(&albumID, &fileURL, &thumbURL)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Medya bulunamadı"}`, http.StatusNotFound)
		return
	}

	if _, err := app.DB.Exec(`DELETE FROM public.face_embeddings WHERE media_id::text = $1`, mediaID); err != nil {
		log.Printf("⚠️ face_embeddings silinemedi: %v", err)
	}

	if _, err := app.DB.Exec(`DELETE FROM public.media WHERE id::text = $1`, mediaID); err != nil {
		log.Printf("❌ media silinemedi: %v", err)
		http.Error(w, `{"status":"error","message":"Silme hatası"}`, http.StatusInternalServerError)
		return
	}

	app.invalidateAlbumCache(albumID)

	go func() {
		if err := deleteFromSupabaseStorage(fileURL); err != nil {
			log.Printf("⚠️ storage orijinal silinemedi: %v", err)
		}
		if thumbURL != "" {
			if err := deleteFromSupabaseStorage(thumbURL); err != nil {
				log.Printf("⚠️ storage thumbnail silinemedi: %v", err)
			}
		}
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"media_id": mediaID,
		"album_id": albumID,
	})
}

func (app *App) invalidateAlbumCache(albumID string) {
	if app.RDB == nil || albumID == "" {
		return
	}

	c := context.Background()
	pattern := fmt.Sprintf("album_detail:%s:*", albumID)

	var keys []string
	iter := app.RDB.Scan(c, 0, pattern, 100).Iterator()
	for iter.Next(c) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		log.Printf("⚠️ cache scan: %v", err)
		return
	}

	if len(keys) == 0 {
		return
	}

	if err := app.RDB.Del(c, keys...).Err(); err != nil {
		log.Printf("⚠️ cache del: %v", err)
		return
	}

	log.Printf("🧹 cache silindi: %d key (album=%s)", len(keys), albumID)
}
