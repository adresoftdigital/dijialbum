package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq" // PostgreSQL Sürücüsü
)

// Albüm Veri Yapısı (Struct)
// Neden json etiketleri var?: Go dilindeki büyük harfli değişken adlarını
// Flutter'ın anlayacağı küçük harfli JSON formatına dönüştürmek için (`json:"title"`)
type Album struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	CoverPhotoURL string    `json:"cover_photo_url"`
	CreatedAt     time.Time `json:"created_at"`
	IsHot         bool      `json:"is_hot"` // Mobil uygulamaya bu verinin sıcak olup olmadığını bildirir
}

// Fotoğraf ve Video Veri Yapısı
type Media struct {
	ID              string `json:"id"`
	AlbumID         string `json:"album_id"`
	URL             string `json:"url"`
	ThumbnailURL    string `json:"thumbnail_url"`
	MediaType       string `json:"media_type"` // 'photo' veya 'video'
	DurationSeconds int    `json:"duration_seconds"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
}

type App struct {
	DB *sql.DB
}

func main() {
	// Supabase PostgreSQL Bağlantı Cümlesi
	// Render veya local ortamdan gelen DATABASE_URL'i okur
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL çevre değişkeni tanımlanmamış!")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Veritabanı bağlantı hatası: %v", err)
	}
	defer db.Close()

	app := &App{DB: db}

	// API Rotaları (HTTP Endpoints)
	http.HandleFunc("/api/v1/albums/hot", app.getHotAlbumsHandler)   // 1. Sıcak Albümler
	http.HandleFunc("/api/v1/albums/cold", app.getColdAlbumsHandler) // 2. Soğuk Albümler
	http.HandleFunc("/api/v1/album/media", app.getAlbumMediaHandler) // 3. Albüm İçi Fotoğraf/Videolar

	port := ":8080"
	fmt.Printf("🚀 Go Backend Sunucusu %s portunda çalışıyor...\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

// 1. Son 3 Günün Sıcak Albümlerini Getiren Fonksiyon
func (app *App) getHotAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// SQL: Sadece created_at >= Son 3 Gün
	query := `
		SELECT id, title, COALESCE(cover_photo_url, ''), created_at
		FROM albums
		WHERE created_at >= NOW() - INTERVAL '3 days'
		ORDER BY created_at DESC
		LIMIT 50;
	`

	rows, err := app.DB.Query(query)
	if err != nil {
		http.Error(w, "Veri çekme hatası: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var hotAlbums []Album
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.Title, &a.CoverPhotoURL, &a.CreatedAt); err != nil {
			continue
		}
		a.IsHot = true // Sıcak veri bayrağı
		hotAlbums = append(hotAlbums, a)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"type":   "HOT_DATA_CACHE",
		"count":  len(hotAlbums),
		"albums": hotAlbums,
	})
}

// 2. 3 Günü Geçmiş Soğuk Albümleri Getiren Fonksiyon
func (app *App) getColdAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// SQL: 3 günden eski veriler (Sayfalamalı - LIMIT 20)
	query := `
		SELECT id, title, COALESCE(cover_photo_url, ''), created_at
		FROM albums
		WHERE created_at < NOW() - INTERVAL '3 days'
		ORDER BY created_at DESC
		LIMIT 20;
	`

	rows, err := app.DB.Query(query)
	if err != nil {
		http.Error(w, "Veri çekme hatası: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var coldAlbums []Album
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.Title, &a.CoverPhotoURL, &a.CreatedAt); err != nil {
			continue
		}
		a.IsHot = false
		coldAlbums = append(coldAlbums, a)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"type":   "COLD_DATA_ARCHIVE",
		"count":  len(coldAlbums),
		"albums": coldAlbums,
	})
}

// 3. Seçilen Albümün İçindeki Tüm Fotoğraf ve Videoları Listeleyen Fonksiyon
func (app *App) getAlbumMediaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	albumID := r.URL.Query().Get("album_id")

	if albumID == "" {
		http.Error(w, "album_id parametresi eksik", http.StatusBadRequest)
		return
	}

	query := `
		SELECT id, album_id, url, thumbnail_url, media_type, duration_seconds, width, height
		FROM media
		WHERE album_id = $1
		ORDER BY created_at ASC;
	`

	rows, err := app.DB.Query(query, albumID)
	if err != nil {
		http.Error(w, "Medya verileri çekilemedi", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var mediaList []Media
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.ID, &m.AlbumID, &m.URL, &m.ThumbnailURL, &m.MediaType, &m.DurationSeconds, &m.Width, &m.Height); err != nil {
			continue
		}
		mediaList = append(mediaList, m)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"album_id": albumID,
		"count":    len(mediaList),
		"media":    mediaList,
	})
}
