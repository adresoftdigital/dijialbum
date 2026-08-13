package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func supabaseAuthBase() string {
	return strings.TrimRight(os.Getenv("SUPABASE_URL"), "/") + "/auth/v1"
}

func supabaseAnonKey() string {
	return os.Getenv("SUPABASE_ANON_KEY")
}

func supabaseServiceKey() string {
	return os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	FullName    string `json:"full_name"`
	Phone       string `json:"phone"`
	AccountType string `json:"account_type"` // individual | organization
}

type loginRequest struct {
	Email           string `json:"email"`
	Password        string `json:"password"`
	RememberMe      bool   `json:"remember_me"`
	PasswordVersion int    `json:"password_version"` // client'ta kayıtlı version (0 = yok)
}

type verifyRequest struct {
	Email string `json:"email"`
	Token string `json:"token"` // e-posta kodu
}

func (app *App) authRegisterHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	req.Phone = strings.TrimSpace(req.Phone)
	if req.AccountType != "organization" {
		req.AccountType = "individual"
	}

	if req.Email == "" || len(req.Password) < 6 || req.FullName == "" {
		http.Error(w, `{"status":"error","message":"Ad, e-posta ve en az 6 karakter şifre zorunlu"}`, http.StatusBadRequest)
		return
	}

	payload := map[string]interface{}{
		"email":    req.Email,
		"password": req.Password,
		"data": map[string]string{
			"full_name":    req.FullName,
			"phone":        req.Phone,
			"account_type": req.AccountType,
		},
	}
	body, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequest(http.MethodPost, supabaseAuthBase()+"/signup", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", supabaseAnonKey())
	httpReq.Header.Set("Authorization", "Bearer "+supabaseAnonKey())

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.Printf("register: %v", err)
		http.Error(w, `{"status":"error","message":"Kayıt servisi hatası"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		log.Printf("register fail %d: %s", resp.StatusCode, string(respBody))
		msg := "Kayıt başarısız"
		var er map[string]interface{}
		if json.Unmarshal(respBody, &er) == nil {
			if m, ok := er["msg"].(string); ok && m != "" {
				msg = m
			} else if m, ok := er["error_description"].(string); ok && m != "" {
				msg = m
			} else if m, ok := er["msg"].(string); ok {
				msg = m
			}
		}
		w.WriteHeader(resp.StatusCode)
		json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": msg})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Kayıt alındı. E-postanıza gelen doğrulama kodunu girin.",
		"email":   req.Email,
	})
}

func (app *App) authVerifyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Token = strings.TrimSpace(req.Token)
	if req.Email == "" || req.Token == "" {
		http.Error(w, `{"status":"error","message":"E-posta ve kod zorunlu"}`, http.StatusBadRequest)
		return
	}

	// Supabase: type=signup OTP doğrulama
	payload := map[string]string{
		"email": req.Email,
		"token": req.Token,
		"type":  "signup",
	}
	body, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequest(http.MethodPost, supabaseAuthBase()+"/verify", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", supabaseAnonKey())
	httpReq.Header.Set("Authorization", "Bearer "+supabaseAnonKey())

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Doğrulama servisi hatası"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		log.Printf("verify fail: %s", string(respBody))
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "Kod geçersiz veya süresi dolmuş",
		})
		return
	}

	var auth map[string]interface{}
	_ = json.Unmarshal(respBody, &auth)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "success",
		"message":       "E-posta doğrulandı",
		"access_token":  auth["access_token"],
		"refresh_token": auth["refresh_token"],
		"user":          auth["user"],
	})
}

func (app *App) authLoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		http.Error(w, `{"status":"error","message":"E-posta ve şifre zorunlu"}`, http.StatusBadRequest)
		return
	}

	payload := map[string]string{
		"email":    req.Email,
		"password": req.Password,
	}
	body, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequest(
		http.MethodPost,
		supabaseAuthBase()+"/token?grant_type=password",
		bytes.NewReader(body),
	)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", supabaseAnonKey())
	httpReq.Header.Set("Authorization", "Bearer "+supabaseAnonKey())

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Giriş servisi hatası"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "E-posta veya şifre hatalı / e-posta doğrulanmamış olabilir",
		})
		return
	}

	var auth map[string]interface{}
	_ = json.Unmarshal(respBody, &auth)

	user, _ := auth["user"].(map[string]interface{})
	userID, _ := user["id"].(string)

	var fullName, phone, accountType string
	var passwordVersion int
	err = app.DB.QueryRow(`
		SELECT COALESCE(full_name,''), COALESCE(phone,''), COALESCE(account_type,'individual'), password_version
		FROM public.profiles WHERE id::text = $1
	`, userID).Scan(&fullName, &phone, &accountType, &passwordVersion)
	if err != nil {
		// profil yoksa varsayılan
		passwordVersion = 1
	}

	// Beni hatırla + şifre değişti mi?
	if req.PasswordVersion > 0 && req.PasswordVersion != passwordVersion {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"code":    "PASSWORD_CHANGED",
			"message": "Şifreniz değiştirilmiş. Lütfen tekrar giriş yapın.",
		})
		return
	}

	_, _ = app.DB.Exec(`
		UPDATE public.profiles SET last_login_at = $1, updated_at = $1 WHERE id::text = $2
	`, time.Now().UTC(), userID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":           "success",
		"access_token":     auth["access_token"],
		"refresh_token":    auth["refresh_token"],
		"expires_in":       auth["expires_in"],
		"user_id":          userID,
		"email":            req.Email,
		"full_name":        fullName,
		"phone":            phone,
		"account_type":     accountType,
		"password_version": passwordVersion,
		"remember_me":      req.RememberMe,
	})
}

// map typo fix - use map[string]string in login error above
