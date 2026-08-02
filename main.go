package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

type Album struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	CoverPhotoURL string    `json:"cover_photo_url"`
	CreatedAt     time.Time `json:"created_at"`
	IsHot         bool      `json:"is_hot"`
}

type App struct {
	DB  *sql.DB
	RDB *redis.Client
}

func main() {
	// 1. Supabase Bağlantısı
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL ortam değişkeni bulunamadı!")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Veritabanı bağlantı hatası: %v", err)
	}
	defer db.Close()

	// 2. Upstash Redis Bağlantısı
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

	http.HandleFunc("/api/v1/albums/hot", app.getHotAlbumsHandler)

	port := ":8080"
	fmt.Printf("🚀 Go Backend Sunucusu %s portunda çalışıyor...\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func (app *App) getHotAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cacheKey := "hot_albums_cache"

	// A) Önce Redis Cache Kontrol Et
	if app.RDB != nil {
		cachedData, err := app.RDB.Get(ctx, cacheKey).Result()
		if err == nil && cachedData != "" {
			// Cache bulundu! Doğrudan Redis'ten dön
			w.Header().Set("X-Cache", "HIT_REDIS")
			w.Write([]byte(cachedData))
			return
		}
	}

	// B) Cache yoksa Supabase Veritabanından Çek
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

	// C) Elde edilen veriyi 5 dakikalığına Redis'e kaydet
	if app.RDB != nil {
		app.RDB.Set(ctx, cacheKey, jsonBytes, 5*time.Minute)
	}

	w.Header().Set("X-Cache", "MISS_SUPABASE")
	w.Write(jsonBytes)
}
