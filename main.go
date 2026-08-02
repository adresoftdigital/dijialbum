package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

// Veritabanı Modelleri
type Album struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	CoverPhotoURL string    `json:"cover_photo_url"`
	CreatedAt     time.Time `json:"created_at"`
	IsHot         bool      `json:"is_hot,omitempty"`
}

type MediaItem struct {
	ID           string    `json:"id"`
	AlbumID      string    `json:"album_id"`
	URL          string    `json:"url"`
	ThumbnailURL string    `json:"thumbnail_url"`
	Title        string    `json:"title"`
	IsVideo      bool      `json:"is_video"`
	IsFavorite   bool      `json:"is_favorite"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Duration     int       `json:"duration"`
	Size         int64     `json:"size"`
	CreatedAt    time.Time `json:"created_at"`
}

type AlbumDetailResponse struct {
	Status  string      `json:"status"`
	Album   Album       `json:"album"`
	Media   []MediaItem `json:"media"`
	Page    int         `json:"page"`
	Limit   int         `json:"limit"`
	HasMore bool        `json:"has_more"`
}

type App struct {
	DB  *sql.DB
	RDB *redis.Client
}

func main() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL ortam değişkeni bulunamadı!")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Veritabanı bağlantı hatası: %v", err)
	}
	defer db.Close()

	redisURL := os.Getenv("REDIS_URL")
	var rdb *redis.Client

	if redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Printf("Redis URL ayrıştırma hatası: %v", err)
		} else {
			rdb = redis.NewClient(opt)
			log.Println("⚡ Upstash Redis bağlantısı yapılandırıldı.")
		}
	} else {
		log.Println("⚠️ REDIS_URL bulunamadı, sistem sadece Supabase ile çalışacak.")
	}

	app := &App{DB: db, RDB: rdb}

	// CORS ve Handler Tanımlamaları
	http.HandleFunc("/api/v1/albums/hot", app.corsMiddleware(app.getHotAlbumsHandler))
	http.HandleFunc("/api/v1/album-detail", app.corsMiddleware(app.getAlbumDetailHandler))

	port := ":8080"
	fmt.Printf("🚀 Go Backend Sunucusu %s portunda çalışıyor...\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

// CORS Ayarı (Flutter Web / Desktop / Mobile erişimi için)
func (app *App) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func (app *App) getHotAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cacheKey := "hot_albums_cache"

	if app.RDB != nil {
		cachedData, err := app.RDB.Get(ctx, cacheKey).Result()
		if err == nil && cachedData != "" {
			w.Header().Set("X-Cache", "HIT_REDIS")
			w.Write([]byte(cachedData))
			return
		}
	}

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
		a.IsHot = true
		hotAlbums = append(hotAlbums, a)
	}

	responsePayload := map[string]interface{}{
		"status": "success",
		"type":   "HOT_DATA_CACHE",
		"count":  len(hotAlbums),
		"albums": hotAlbums,
	}

	jsonBytes, _ := json.Marshal(responsePayload)

	if app.RDB != nil {
		app.RDB.Set(ctx, cacheKey, jsonBytes, 5*time.Minute)
	}

	w.Header().Set("X-Cache", "MISS_SUPABASE")
	w.Write(jsonBytes)
}

// ALBÜM DETAY VE SAYFALAMALI MEDYA ÇEKME HANDLER'I
func (app *App) getAlbumDetailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	albumID := r.URL.Query().Get("album_id")
	if albumID == "" {
		http.Error(w, `{"status":"error","message":"album_id parametresi zorunludur"}`, http.StatusBadRequest)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	offset := (page - 1) * limit

	cacheKey := fmt.Sprintf("album_detail:%s:page:%d:limit:%d", albumID, page, limit)

	// Redis Cache Kontrolü
	if app.RDB != nil {
		cachedData, err := app.RDB.Get(ctx, cacheKey).Result()
		if err == nil && cachedData != "" {
			w.Header().Set("X-Cache", "HIT_REDIS")
			w.Write([]byte(cachedData))
			return
		}
	}

	// 1. Albüm Temel Bilgisi
	var album Album
	albumQuery := `SELECT id, title, COALESCE(cover_photo_url, ''), created_at FROM albums WHERE id = $1`
	err := app.DB.QueryRow(albumQuery, albumID).Scan(&album.ID, &album.Title, &album.CoverPhotoURL, &album.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, `{"status":"error","message":"Albüm bulunamadı"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"status":"error","message":"Veritabanı hatası: `+err.Error()+`"}`, http.StatusInternalServerError)
		}
		return
	}

	// 2. Medya Listesi (Sayfalanmış)
	mediaQuery := `
		SELECT 
			id, album_id, url, COALESCE(thumbnail_url, ''), COALESCE(title, ''), 
			COALESCE(is_video, false), COALESCE(is_favorite, false), 
			COALESCE(width, 0), COALESCE(height, 0), COALESCE(duration, 0), 
			COALESCE(size, 0), created_at
		FROM media
		WHERE album_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := app.DB.Query(mediaQuery, albumID, limit+1, offset)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Medya çekme hatası: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var mediaList []MediaItem
	for rows.Next() {
		var m MediaItem
		err := rows.Scan(
			&m.ID, &m.AlbumID, &m.URL, &m.ThumbnailURL, &m.Title,
			&m.IsVideo, &m.IsFavorite, &m.Width, &m.Height,
			&m.Duration, &m.Size, &m.CreatedAt,
		)
		if err != nil {
			continue
		}
		mediaList = append(mediaList, m)
	}

	hasMore := false
	if len(mediaList) > limit {
		hasMore = true
		mediaList = mediaList[:limit] // Fazladan çekilen kontrol ögesini kes
	}

	responsePayload := AlbumDetailResponse{
		Status:  "success",
		Album:   album,
		Media:   mediaList,
		Page:    page,
		Limit:   limit,
		HasMore: hasMore,
	}

	jsonBytes, _ := json.Marshal(responsePayload)

	// Cache'e Kaydet (3 Dakika)
	if app.RDB != nil {
		app.RDB.Set(ctx, cacheKey, jsonBytes, 3*time.Minute)
	}

	w.Header().Set("X-Cache", "MISS_SUPABASE")
	w.Write(jsonBytes)
}
