package main

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type albumSecurityPublic struct {
	AlbumID    string `json:"album_id"`
	AccessMode string `json:"access_mode"`
	PinHint    string `json:"pin_hint"`
	HasPin     bool   `json:"has_pin"`
}

type setSecurityRequest struct {
	AlbumID    string `json:"album_id"`
	UserID     string `json:"user_id"` // sahip kontrolü (şimdilik)
	AccessMode string `json:"access_mode"`
	Pin        string `json:"pin"` // boş = PIN’i değiştirme
	PinHint    string `json:"pin_hint"`
	ClearPin   bool   `json:"clear_pin"`
}

type verifyPinRequest struct {
	AlbumID  string `json:"album_id"`
	Pin      string `json:"pin"`
	DeviceID string `json:"device_id"`
}

type verifyFaceRequest struct {
	AlbumID   string    `json:"album_id"`
	Embedding []float64 `json:"embedding"`
	Threshold float64   `json:"threshold"`
	DeviceID  string    `json:"device_id"`
}

func (app *App) getAlbumSecurityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	albumID := strings.TrimSpace(r.URL.Query().Get("album_id"))
	if albumID == "" {
		http.Error(w, `{"status":"error","message":"album_id zorunlu"}`, http.StatusBadRequest)
		return
	}

	var mode, hint string
	var pinHash *string
	err := app.DB.QueryRow(`
		SELECT COALESCE(access_mode, 'open'), COALESCE(pin_hint, ''), pin_hash
		FROM public.albums WHERE id::text = $1
	`, albumID).Scan(&mode, &hint, &pinHash)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Albüm yok"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"security": albumSecurityPublic{
			AlbumID:    albumID,
			AccessMode: mode,
			PinHint:    hint,
			HasPin:     pinHash != nil && *pinHash != "",
		},
	})
}

func (app *App) setAlbumSecurityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req setSecurityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.AlbumID = strings.TrimSpace(req.AlbumID)
	req.AccessMode = strings.TrimSpace(req.AccessMode)
	req.Pin = strings.TrimSpace(req.Pin)
	req.PinHint = strings.TrimSpace(req.PinHint)

	allowed := map[string]bool{
		"open": true, "pin": true, "face": true, "pin_or_face": true, "face_media": true,
	}
	if req.AlbumID == "" || !allowed[req.AccessMode] {
		http.Error(w, `{"status":"error","message":"Geçersiz istek"}`, http.StatusBadRequest)
		return
	}

	// Sahiplik (basit)
	if req.UserID != "" {
		var owner string
		_ = app.DB.QueryRow(`SELECT COALESCE(user_id::text,'') FROM public.albums WHERE id::text = $1`, req.AlbumID).Scan(&owner)
		if owner != "" && owner != req.UserID {
			http.Error(w, `{"status":"error","message":"Yetkisiz"}`, http.StatusForbidden)
			return
		}
	}

	needsPin := req.AccessMode == "pin" || req.AccessMode == "pin_or_face"
	if needsPin && req.Pin == "" && !req.ClearPin {
		// mevcut pin var mı?
		var ph *string
		_ = app.DB.QueryRow(`SELECT pin_hash FROM public.albums WHERE id::text = $1`, req.AlbumID).Scan(&ph)
		if ph == nil || *ph == "" {
			http.Error(w, `{"status":"error","message":"Bu mod için 4-8 haneli PIN gerekli"}`, http.StatusBadRequest)
			return
		}
	}

	if req.Pin != "" {
		if len(req.Pin) < 4 || len(req.Pin) > 8 {
			http.Error(w, `{"status":"error","message":"PIN 4-8 haneli olmalı"}`, http.StatusBadRequest)
			return
		}
		for _, c := range req.Pin {
			if c < '0' || c > '9' {
				http.Error(w, `{"status":"error","message":"PIN sadece rakam olmalı"}`, http.StatusBadRequest)
				return
			}
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Pin), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, `{"status":"error","message":"PIN hash hatası"}`, http.StatusInternalServerError)
			return
		}
		_, err = app.DB.Exec(`
			UPDATE public.albums
			SET access_mode = $1, pin_hash = $2, pin_hint = $3
			WHERE id::text = $4
		`, req.AccessMode, string(hash), req.PinHint, req.AlbumID)
		if err != nil {
			log.Printf("set security: %v", err)
			http.Error(w, `{"status":"error","message":"Kayıt başarısız"}`, http.StatusInternalServerError)
			return
		}
	} else if req.ClearPin || req.AccessMode == "open" || req.AccessMode == "face" || req.AccessMode == "face_media" {
		_, err := app.DB.Exec(`
			UPDATE public.albums
			SET access_mode = $1,
			    pin_hash = CASE WHEN $2 THEN NULL ELSE pin_hash END,
			    pin_hint = $3
			WHERE id::text = $4
		`, req.AccessMode, req.ClearPin || req.AccessMode == "open", req.PinHint, req.AlbumID)
		if err != nil {
			http.Error(w, `{"status":"error","message":"Kayıt başarısız"}`, http.StatusInternalServerError)
			return
		}
	} else {
		_, err := app.DB.Exec(`
			UPDATE public.albums SET access_mode = $1, pin_hint = $2 WHERE id::text = $3
		`, req.AccessMode, req.PinHint, req.AlbumID)
		if err != nil {
			http.Error(w, `{"status":"error","message":"Kayıt başarısız"}`, http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (app *App) verifyAlbumPinHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req verifyPinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.AlbumID = strings.TrimSpace(req.AlbumID)
	req.Pin = strings.TrimSpace(req.Pin)

	var pinHash *string
	err := app.DB.QueryRow(`SELECT pin_hash FROM public.albums WHERE id::text = $1`, req.AlbumID).Scan(&pinHash)
	if err != nil || pinHash == nil || *pinHash == "" {
		http.Error(w, `{"status":"error","message":"PIN tanımlı değil"}`, http.StatusBadRequest)
		return
	}

	ok := bcrypt.CompareHashAndPassword([]byte(*pinHash), []byte(req.Pin)) == nil
	_, _ = app.DB.Exec(`
		INSERT INTO public.album_access_logs (album_id, method, success, device_id)
		VALUES ($1::uuid, 'pin', $2, $3)
	`, req.AlbumID, ok, req.DeviceID)

	if !ok {
		http.Error(w, `{"status":"error","message":"PIN hatalı"}`, http.StatusUnauthorized)
		return
	}

	token := randomToken(24)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"access_token": token, // client sessionStorage
		"method":       "pin",
	})
}

// Yüz ile kapı: albümde threshold altında en az 1 eşleşme
func (app *App) verifyAlbumFaceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req verifyFaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	if req.Threshold <= 0 {
		req.Threshold = 0.55
	}
	if len(req.Embedding) < 64 {
		http.Error(w, `{"status":"error","message":"Geçersiz embedding"}`, http.StatusBadRequest)
		return
	}

	// Mevcut face-search ile aynı mantık: eşleşen media var mı?
	// Basit: match_faces veya L2 sorgusu
	vec := floatSliceToVectorLiteral(req.Embedding) // sende varsa helper

	var cnt int
	err := app.DB.QueryRow(`
		SELECT COUNT(*)::int FROM (
			SELECT 1
			FROM public.face_embeddings fe
			WHERE fe.album_id::text = $1
			  AND (fe.embedding <-> $2::vector) < $3
			LIMIT 1
		) t
	`, req.AlbumID, vec, req.Threshold).Scan(&cnt)

	ok := err == nil && cnt > 0
	_, _ = app.DB.Exec(`
		INSERT INTO public.album_access_logs (album_id, method, success, device_id)
		VALUES ($1::uuid, 'face', $2, $3)
	`, req.AlbumID, ok, req.DeviceID)

	if !ok {
		http.Error(w, `{"status":"error","message":"Yüz eşleşmesi bulunamadı"}`, http.StatusUnauthorized)
		return
	}

	// face_media modunda client'a matched id listesi de dönebilirsin (face-search çağrısı)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"access_token": randomToken(24),
		"method":       "face",
	})
}

func floatSliceToVectorLiteral(f []float64) any {
	panic("unimplemented")
}

func randomToken(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}
