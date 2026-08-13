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
