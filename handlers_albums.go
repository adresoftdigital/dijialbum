package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

type AlbumListItem struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	CoverPhotoURL string `json:"cover_photo_url"`
	CreatedAt     string `json:"created_at"`
	IsHot         bool   `json:"is_hot"`
	MediaCount    int    `json:"media_count"`
}

func (app *App) myAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"status":"error","message":"Sadece GET"}`, http.StatusMethodNotAllowed)
		return
	}

	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		// Authorization: Bearer <access_token> — opsiyonel ileride
		http.Error(w, `{"status":"error","message":"user_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	rows, err := app.DB.Query(`
		SELECT
			a.id::text,
			COALESCE(a.title, 'Albüm'),
			COALESCE(
				NULLIF(a.cover_photo_url, ''),
				(
					SELECT COALESCE(NULLIF(m.thumbnail_url, ''), m.url)
					FROM public.media m
					WHERE m.album_id = a.id
					ORDER BY m.created_at DESC
					LIMIT 1
				),
				''
			) AS cover,
			COALESCE(a.created_at::text, ''),
			COALESCE(a.is_hot, false),
			COALESCE((
				SELECT COUNT(*)::int FROM public.media m WHERE m.album_id = a.id
			), 0) AS media_count
		FROM public.albums a
		WHERE a.user_id::text = $1
		ORDER BY a.created_at DESC NULLS LAST
	`, userID)
	if err != nil {
		log.Printf("❌ myAlbums: %v", err)
		// is_hot yoksa sade sorgu dene
		rows, err = app.DB.Query(`
			SELECT
				a.id::text,
				COALESCE(a.title, 'Albüm'),
				COALESCE(
					NULLIF(a.cover_photo_url, ''),
					(
						SELECT COALESCE(NULLIF(m.thumbnail_url, ''), m.url)
						FROM public.media m
						WHERE m.album_id = a.id
						ORDER BY m.created_at DESC
						LIMIT 1
					),
					''
				),
				COALESCE(a.created_at::text, ''),
				false,
				COALESCE((SELECT COUNT(*)::int FROM public.media m WHERE m.album_id = a.id), 0)
			FROM public.albums a
			WHERE a.user_id::text = $1
			ORDER BY a.created_at DESC NULLS LAST
		`, userID)
		if err != nil {
			log.Printf("❌ myAlbums fallback: %v", err)
			http.Error(w, `{"status":"error","message":"Albümler alınamadı"}`, http.StatusInternalServerError)
			return
		}
	}
	defer rows.Close()

	var list []AlbumListItem
	for rows.Next() {
		var a AlbumListItem
		if err := rows.Scan(&a.ID, &a.Title, &a.CoverPhotoURL, &a.CreatedAt, &a.IsHot, &a.MediaCount); err != nil {
			continue
		}
		list = append(list, a)
	}
	if list == nil {
		list = []AlbumListItem{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"albums": list,
	})
}

type createAlbumRequest struct {
	UserID          string `json:"user_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	BackgroundColor string `json:"background_color"`
}

func (app *App) createAlbumHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req createAlbumRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	req.UserID = strings.TrimSpace(req.UserID)
	req.BackgroundColor = strings.TrimSpace(req.BackgroundColor)

	if req.Title == "" || req.UserID == "" {
		http.Error(w, `{"status":"error","message":"title ve user_id zorunlu"}`, http.StatusBadRequest)
		return
	}
	if req.BackgroundColor == "" {
		req.BackgroundColor = "#1A73E8"
	}
	if !strings.HasPrefix(req.BackgroundColor, "#") {
		req.BackgroundColor = "#" + req.BackgroundColor
	}

	var id string
	err := app.DB.QueryRow(`
		INSERT INTO public.albums (title, description, background_color, user_id, cover_photo_url)
		VALUES ($1, $2, $3, $4::uuid, '')
		RETURNING id::text
	`, req.Title, req.Description, req.BackgroundColor, req.UserID).Scan(&id)

	if err != nil {
		log.Printf("❌ createAlbum: %v", err)
		// description / background_color yoksa minimal insert
		err2 := app.DB.QueryRow(`
			INSERT INTO public.albums (title, user_id, cover_photo_url)
			VALUES ($1, $2::uuid, '')
			RETURNING id::text
		`, req.Title, req.UserID).Scan(&id)
		if err2 != nil {
			log.Printf("❌ createAlbum fallback: %v", err2)
			http.Error(w, `{"status":"error","message":"Albüm oluşturulamadı"}`, http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"album_id": id,
		"title":    req.Title,
	})
}
