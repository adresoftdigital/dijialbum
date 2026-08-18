package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
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

type createSocialRequest struct {
	AlbumID      string `json:"album_id"`
	Platform     string `json:"platform"`
	PostURL      string `json:"post_url"`
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnail_url"`
}

func (app *App) createSocialPostHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req createSocialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}

	req.AlbumID = strings.TrimSpace(req.AlbumID)
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	req.PostURL = strings.TrimSpace(req.PostURL)
	req.Title = strings.TrimSpace(req.Title)
	req.ThumbnailURL = strings.TrimSpace(req.ThumbnailURL)

	allowed := map[string]bool{
		"instagram": true, "facebook": true, "x": true,
		"tiktok": true, "youtube": true, "other": true,
	}
	if req.AlbumID == "" || req.PostURL == "" || !allowed[req.Platform] {
		http.Error(w, `{"status":"error","message":"album_id, platform ve post_url zorunlu"}`, http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.PostURL, "http://") && !strings.HasPrefix(req.PostURL, "https://") {
		http.Error(w, `{"status":"error","message":"Geçerli bir URL girin"}`, http.StatusBadRequest)
		return
	}

	var id string
	err := app.DB.QueryRow(`
		INSERT INTO public.album_social_posts (album_id, platform, post_url, title, thumbnail_url)
		VALUES ($1::uuid, $2, $3, $4, $5)
		RETURNING id::text
	`, req.AlbumID, req.Platform, req.PostURL, req.Title, req.ThumbnailURL).Scan(&id)
	if err != nil {
		log.Printf("❌ create social: %v", err)
		http.Error(w, `{"status":"error","message":"Kayıt başarısız"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"id":     id,
	})
}

// DELETE /api/v1/album-social?id=
func (app *App) deleteSocialPostHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece DELETE/POST"}`, http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, `{"status":"error","message":"id zorunlu"}`, http.StatusBadRequest)
		return
	}
	res, err := app.DB.Exec(`DELETE FROM public.album_social_posts WHERE id::text = $1`, id)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Silinemedi"}`, http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, `{"status":"error","message":"Kayıt yok"}`, http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
