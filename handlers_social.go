package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type SocialPost struct {
	ID           string `json:"id"`
	AlbumID      string `json:"album_id"`
	Platform     string `json:"platform"`
	PostURL      string `json:"post_url"`
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnail_url"`
	CreatedAt    string `json:"created_at"`
}

func (app *App) listSocialPostsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, `{"status":"error","message":"Sadece GET"}`, http.StatusMethodNotAllowed)
		return
	}

	albumID := r.URL.Query().Get("album_id")
	if albumID == "" {
		http.Error(w, `{"status":"error","message":"album_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	rows, err := app.DB.Query(`
		SELECT id::text, album_id::text, platform, post_url,
		       COALESCE(title,''), COALESCE(thumbnail_url,''), created_at::text
		FROM public.album_social_posts
		WHERE album_id::text = $1
		ORDER BY created_at DESC
	`, albumID)
	if err != nil {
		log.Printf("❌ social list: %v", err)
		http.Error(w, `{"status":"error","message":"Sorgu hatası"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []SocialPost
	for rows.Next() {
		var p SocialPost
		if err := rows.Scan(
			&p.ID, &p.AlbumID, &p.Platform, &p.PostURL,
			&p.Title, &p.ThumbnailURL, &p.CreatedAt,
		); err != nil {
			continue
		}
		list = append(list, p)
	}
	if list == nil {
		list = []SocialPost{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"posts":  list,
	})
}
