package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

func (app *App) uploadMediaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, `{"status":"error","message":"Form okunamadı"}`, http.StatusBadRequest)
		return
	}

	albumID := r.FormValue("album_id")
	if albumID == "" {
		http.Error(w, `{"status":"error","message":"album_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	// Yüz embedding'leri (Flutter'dan JSON string olarak gelir)
	// Örnek: "[[0.12,-0.05,...128 sayı...]]"
	embeddingsJSON := r.FormValue("embeddings")
	var embeddings [][]float64
	if embeddingsJSON != "" {
		if err := json.Unmarshal([]byte(embeddingsJSON), &embeddings); err != nil {
			log.Printf("⚠️ embeddings JSON parse hatası: %v", err)
			embeddings = nil
		}
	}

	// Albüm kontrolü
	var exists bool
	err := app.DB.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM public.albums WHERE id::text = $1)`,
		albumID,
	).Scan(&exists)
	if err != nil || !exists {
		http.Error(w, `{"status":"error","message":"Albüm bulunamadı"}`, http.StatusNotFound)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		http.Error(w, `{"status":"error","message":"Dosya yok"}`, http.StatusBadRequest)
		return
	}

	var uploaded []map[string]interface{}
	var errors []string

	for _, fh := range files {
		item, err := app.saveOneMedia(fh, albumID, embeddings)
		if err != nil {
			msg := fmt.Sprintf("%s: %v", fh.Filename, err)
			log.Printf("❌ %s", msg)
			errors = append(errors, msg)
			continue
		}
		uploaded = append(uploaded, item)
	}

	if len(uploaded) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Hiçbir dosya yüklenemedi",
			"errors":  errors,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"count":  len(uploaded),
		"media":  uploaded,
		"errors": errors,
	})
}

func (app *App) saveOneMedia(
	fh *multipart.FileHeader,
	albumID string,
	embeddings [][]float64,
) (map[string]interface{}, error) {

	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}

	mediaID := uuid.New().String()
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if ext == "" {
		ext = ".jpg"
	}

	contentType := fh.Header.Get("Content-Type")
	isVideo := strings.HasPrefix(contentType, "video/") ||
		ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".avi"

	mediaType := "photo"
	if isVideo {
		mediaType = "video"
	}

	var width, height int
	var thumbURL string

	originalPath := fmt.Sprintf("%s/originals/%s%s", albumID, mediaID, ext)
	originalURL, err := uploadToSupabaseStorage(originalPath, data, contentType)
	if err != nil {
		return nil, fmt.Errorf("storage orijinal: %w", err)
	}

	if !isVideo {
		thumbBytes, w, h, err := createThumbnail(data)
		if err == nil {
			width, height = w, h
			thumbPath := fmt.Sprintf("%s/thumbnails/%s.jpg", albumID, mediaID)
			thumbURL, err = uploadToSupabaseStorage(thumbPath, thumbBytes, "image/jpeg")
			if err != nil {
				log.Printf("⚠️ thumbnail atlandı: %v", err)
			}
		}
	}

	_, err = app.DB.Exec(`
		INSERT INTO public.media
			(id, album_id, url, thumbnail_url, media_type, duration_seconds, width, height, file_size_bytes)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8)
	`, mediaID, albumID, originalURL, thumbURL, mediaType, width, height, len(data))

	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}

	// ✅ Yüz embedding'lerini kaydet (sadece fotoğraflarda)
	if !isVideo && len(embeddings) > 0 {
		app.saveFaceEmbeddings(mediaID, albumID, embeddings)
	}

	runtime.GC()

	return map[string]interface{}{
		"id":            mediaID,
		"album_id":      albumID,
		"url":           originalURL,
		"thumbnail_url": thumbURL,
		"media_type":    mediaType,
		"width":         width,
		"height":        height,
		"size":          len(data),
	}, nil
}
