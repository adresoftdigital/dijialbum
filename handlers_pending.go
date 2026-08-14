package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type PendingItem struct {
	ID           string `json:"id"`
	AlbumID      string `json:"album_id"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	MediaType    string `json:"media_type"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

type pendingIDRequest struct {
	ID string `json:"id"`
}

// POST /api/v1/pending/upload
func (app *App) pendingUploadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, `{"status":"error","message":"Form okunamadı"}`, http.StatusBadRequest)
		return
	}

	albumID := strings.TrimSpace(r.FormValue("album_id"))
	if albumID == "" {
		http.Error(w, `{"status":"error","message":"album_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, `{"status":"error","message":"Dosya yok"}`, http.StatusBadRequest)
		return
	}

	var saved []PendingItem

	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			log.Printf("❌ pending open: %v", err)
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil || len(data) == 0 {
			log.Printf("❌ pending read: %v", err)
			continue
		}

		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext == "" {
			ext = ".jpg"
		}

		mediaType := "photo"
		if ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".avi" {
			mediaType = "video"
		}

		id := uuid.New().String()
		path := fmt.Sprintf("pending/%s/%s%s", albumID, id, ext)

		contentType := fh.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		publicURL, err := uploadToSupabaseStorage(path, data, contentType)
		if err != nil {
			log.Printf("❌ pending storage: %v", err)
			continue
		}

		thumbURL := publicURL
		width, height := 0, 0

		if mediaType == "photo" {
			if thumbBytes, w, h, tErr := createThumbnail(data); tErr == nil && len(thumbBytes) > 0 {
				width, height = w, h
				thumbPath := fmt.Sprintf("pending/%s/%s_thumb.jpg", albumID, id)
				if tURL, uErr := uploadToSupabaseStorage(thumbPath, thumbBytes, "image/jpeg"); uErr == nil {
					thumbURL = tURL
				}
			}
		}

		var rowID string
		err = app.DB.QueryRow(`
			INSERT INTO public.pending_media (
				id, album_id, storage_path, url, thumbnail_url,
				media_type, file_name, file_size, width, height, status
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, $5,
				$6, $7, $8, $9, $10, 'pending'
			)
			RETURNING id::text
		`, id, albumID, path, publicURL, thumbURL,
			mediaType, fh.Filename, int64(len(data)), width, height,
		).Scan(&rowID)

		if err != nil {
			log.Printf("❌ pending insert: %v", err)
			continue
		}

		saved = append(saved, PendingItem{
			ID:           rowID,
			AlbumID:      albumID,
			URL:          publicURL,
			ThumbnailURL: thumbURL,
			MediaType:    mediaType,
			FileName:     fh.Filename,
			FileSize:     int64(len(data)),
			Width:        width,
			Height:       height,
			Status:       "pending",
		})
	}

	if len(saved) == 0 {
		http.Error(w, `{"status":"error","message":"Hiçbir dosya yüklenemedi"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Dosyalar onaya gönderildi",
		"items":   saved,
	})
}

// GET /api/v1/pending?album_id=
func (app *App) pendingListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"status":"error","message":"Sadece GET"}`, http.StatusMethodNotAllowed)
		return
	}

	albumID := strings.TrimSpace(r.URL.Query().Get("album_id"))
	if albumID == "" {
		http.Error(w, `{"status":"error","message":"album_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	app.cleanupExpiredPending()

	rows, err := app.DB.Query(`
		SELECT
			id::text,
			album_id::text,
			url,
			COALESCE(thumbnail_url, ''),
			media_type,
			COALESCE(file_name, ''),
			file_size,
			COALESCE(width, 0),
			COALESCE(height, 0),
			status,
			created_at::text
		FROM public.pending_media
		WHERE album_id::text = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, albumID)
	if err != nil {
		log.Printf("❌ pending list: %v", err)
		http.Error(w, `{"status":"error","message":"Liste alınamadı"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []PendingItem
	for rows.Next() {
		var p PendingItem
		if err := rows.Scan(
			&p.ID, &p.AlbumID, &p.URL, &p.ThumbnailURL, &p.MediaType,
			&p.FileName, &p.FileSize, &p.Width, &p.Height, &p.Status, &p.CreatedAt,
		); err != nil {
			log.Printf("❌ pending scan: %v", err)
			continue
		}
		list = append(list, p)
	}
	if list == nil {
		list = []PendingItem{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"items":  list,
	})
}

// POST /api/v1/pending/approve  { "id": "..." }
func (app *App) pendingApproveHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req pendingIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		http.Error(w, `{"status":"error","message":"id zorunlu"}`, http.StatusBadRequest)
		return
	}

	var albumID, publicURL, thumbURL, mediaType, fileName string
	var fileSize int64
	var width, height int

	err := app.DB.QueryRow(`
		SELECT
			album_id::text,
			url,
			COALESCE(thumbnail_url, ''),
			media_type,
			COALESCE(file_name, ''),
			file_size,
			COALESCE(width, 0),
			COALESCE(height, 0)
		FROM public.pending_media
		WHERE id::text = $1 AND status = 'pending'
	`, req.ID).Scan(
		&albumID, &publicURL, &thumbURL, &mediaType,
		&fileName, &fileSize, &width, &height,
	)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Bekleyen medya bulunamadı"}`, http.StatusNotFound)
		return
	}

	mediaID := uuid.New().String()
	_, err = app.DB.Exec(`
		INSERT INTO public.media (
			id, album_id, url, thumbnail_url, media_type,
			duration_seconds, width, height, file_size_bytes
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5,
			0, $6, $7, $8
		)
	`, mediaID, albumID, publicURL, thumbURL, mediaType, width, height, fileSize)
	if err != nil {
		log.Printf("❌ approve media insert: %v", err)
		http.Error(w, `{"status":"error","message":"Media kaydı oluşturulamadı"}`, http.StatusInternalServerError)
		return
	}

	_, _ = app.DB.Exec(`DELETE FROM public.pending_media WHERE id::text = $1`, req.ID)

	if app.RDB != nil {
		app.invalidateAlbumCache(albumID)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"media_id": mediaID,
	})
}

// POST /api/v1/pending/reject  { "id": "..." }
func (app *App) pendingRejectHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req pendingIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		http.Error(w, `{"status":"error","message":"id zorunlu"}`, http.StatusBadRequest)
		return
	}

	var publicURL, thumbURL string
	err := app.DB.QueryRow(`
		SELECT COALESCE(url, ''), COALESCE(thumbnail_url, '')
		FROM public.pending_media
		WHERE id::text = $1 AND status = 'pending'
	`, req.ID).Scan(&publicURL, &thumbURL)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Kayıt yok"}`, http.StatusNotFound)
		return
	}

	go func() {
		if publicURL != "" {
			if err := deleteFromSupabaseStorage(publicURL); err != nil {
				log.Printf("⚠️ pending reject storage: %v", err)
			}
		}
		if thumbURL != "" && thumbURL != publicURL {
			if err := deleteFromSupabaseStorage(thumbURL); err != nil {
				log.Printf("⚠️ pending reject thumb: %v", err)
			}
		}
	}()

	_, _ = app.DB.Exec(`DELETE FROM public.pending_media WHERE id::text = $1`, req.ID)

	json.NewEncoder(w).Encode(map[string]string{
		"status": "success",
	})
}

func (app *App) cleanupExpiredPending() {
	rows, err := app.DB.Query(`
		SELECT id::text, COALESCE(url, ''), COALESCE(thumbnail_url, '')
		FROM public.pending_media
		WHERE status = 'pending'
		  AND created_at < NOW() - INTERVAL '24 hours'
	`)
	if err != nil {
		log.Printf("⚠️ pending cleanup query: %v", err)
		return
	}
	defer rows.Close()

	type expired struct {
		id, url, thumb string
	}
	var list []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.id, &e.url, &e.thumb); err != nil {
			continue
		}
		list = append(list, e)
	}

	for _, e := range list {
		if e.url != "" {
			_ = deleteFromSupabaseStorage(e.url)
		}
		if e.thumb != "" && e.thumb != e.url {
			_ = deleteFromSupabaseStorage(e.thumb)
		}
		_, _ = app.DB.Exec(`DELETE FROM public.pending_media WHERE id::text = $1`, e.id)
	}

	if len(list) > 0 {
		log.Printf("🧹 pending cleanup: %d kayıt silindi", len(list))
	}
}
