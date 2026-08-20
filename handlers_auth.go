package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

type changePasswordRequest struct {
	AccessToken     string `json:"access_token"`
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (app *App) changePasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.CurrentPassword == "" || len(req.NewPassword) < 6 {
		http.Error(w, `{"status":"error","message":"Geçersiz istek"}`, http.StatusBadRequest)
		return
	}

	// 1) Mevcut şifre doğru mu?
	loginBody, _ := json.Marshal(map[string]string{
		"email": req.Email, "password": req.CurrentPassword,
	})
	loginReq, _ := http.NewRequest(http.MethodPost,
		supabaseAuthBase()+"/token?grant_type=password", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("apikey", supabaseAnonKey())
	loginReq.Header.Set("Authorization", "Bearer "+supabaseAnonKey())
	loginResp, err := http.DefaultClient.Do(loginReq)
	if err != nil || loginResp.StatusCode >= 300 {
		if loginResp != nil {
			loginResp.Body.Close()
		}
		http.Error(w, `{"status":"error","message":"Mevcut şifre hatalı"}`, http.StatusUnauthorized)
		return
	}
	loginBytes, _ := io.ReadAll(loginResp.Body)
	loginResp.Body.Close()
	var loginData map[string]interface{}
	_ = json.Unmarshal(loginBytes, &loginData)
	user, _ := loginData["user"].(map[string]interface{})
	userID, _ := user["id"].(string)
	accessToken, _ := loginData["access_token"].(string)
	if accessToken == "" {
		accessToken = req.AccessToken
	}

	// 2) Yeni şifre (Supabase user update)
	updBody, _ := json.Marshal(map[string]string{"password": req.NewPassword})
	updReq, _ := http.NewRequest(http.MethodPut, supabaseAuthBase()+"/user", bytes.NewReader(updBody))
	updReq.Header.Set("Content-Type", "application/json")
	updReq.Header.Set("apikey", supabaseAnonKey())
	updReq.Header.Set("Authorization", "Bearer "+accessToken)
	updResp, err := http.DefaultClient.Do(updReq)
	if err != nil {
		http.Error(w, `{"status":"error","message":"Şifre güncellenemedi"}`, http.StatusBadGateway)
		return
	}
	defer updResp.Body.Close()
	if updResp.StatusCode >= 300 {
		b, _ := io.ReadAll(updResp.Body)
		log.Printf("change-password: %s", string(b))
		http.Error(w, `{"status":"error","message":"Şifre güncellenemedi"}`, http.StatusBadRequest)
		return
	}

	// 3) password_version++
	if userID != "" {
		_, _ = app.DB.Exec(`
			UPDATE public.profiles
			SET password_version = password_version + 1, updated_at = NOW()
			WHERE id::text = $1
		`, userID)
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Şifre güncellendi. Lütfen tekrar giriş yapın.",
	})
}

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Email       string `json:"email"`
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (app *App) forgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req forgotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		http.Error(w, `{"status":"error","message":"Geçerli e-posta girin"}`, http.StatusBadRequest)
		return
	}

	// Kullanıcı var mı? (profiles veya auth — service role ile opsiyonel)
	code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	if code == "000000" {
		code = "123456"
	}
	exp := time.Now().UTC().Add(15 * time.Minute)

	_, _ = app.DB.Exec(`UPDATE public.password_reset_codes SET used = true WHERE email = $1 AND used = false`, email)
	_, err := app.DB.Exec(`
		INSERT INTO public.password_reset_codes (email, code, expires_at)
		VALUES ($1, $2, $3)
	`, email, code, exp)
	if err != nil {
		log.Printf("forgot insert: %v", err)
		http.Error(w, `{"status":"error","message":"Kod oluşturulamadı"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("🔐 PASSWORD RESET CODE for %s => %s (15 dk)", email, code)

	mailErr := app.sendPasswordResetEmail(email, code)

	resp := map[string]interface{}{
		"status":  "success",
		"message": "Kod e-posta adresinize gönderildi.",
	}
	if mailErr != nil {
		resp["message"] = "Kod oluşturuldu; e-posta şu an gönderilemedi. Log kontrol edin."
		// Geliştirme kolaylığı
		if os.Getenv("APP_ENV") == "development" || os.Getenv("APP_ENV") == "dev" {
			resp["debug_code"] = code
		}
	}

	json.NewEncoder(w).Encode(resp)
}

func (app *App) resetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"status":"error","message":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"error","message":"JSON hatası"}`, http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	token := strings.TrimSpace(req.Token)
	if email == "" || token == "" || len(req.NewPassword) < 6 {
		http.Error(w, `{"status":"error","message":"E-posta, kod ve yeni şifre (min 6) zorunlu"}`, http.StatusBadRequest)
		return
	}

	var dbCode string
	var exp time.Time
	var used bool
	err := app.DB.QueryRow(`
		SELECT code, expires_at, used FROM public.password_reset_codes
		WHERE email = $1 AND used = false
		ORDER BY created_at DESC LIMIT 1
	`, email).Scan(&dbCode, &exp, &used)
	if err != nil || used || time.Now().UTC().After(exp) || dbCode != token {
		http.Error(w, `{"status":"error","message":"Kod geçersiz veya süresi dolmuş"}`, http.StatusBadRequest)
		return
	}

	// Auth user id — service role
	userID, err := app.findAuthUserIDByEmail(email)
	if err != nil || userID == "" {
		http.Error(w, `{"status":"error","message":"Kullanıcı bulunamadı"}`, http.StatusNotFound)
		return
	}

	if err := app.adminUpdateUserPassword(userID, req.NewPassword); err != nil {
		log.Printf("admin password: %v", err)
		http.Error(w, `{"status":"error","message":"Şifre güncellenemedi"}`, http.StatusBadGateway)
		return
	}

	_, _ = app.DB.Exec(`UPDATE public.password_reset_codes SET used = true WHERE email = $1`, email)
	_, _ = app.DB.Exec(`
		UPDATE public.profiles
		SET password_version = COALESCE(password_version, 0) + 1
		WHERE id::text = $1
	`, userID)

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Şifre güncellendi",
	})
}

func (app *App) findAuthUserIDByEmail(email string) (string, error) {
	// 1) profiles'ta email varsa
	var id string
	err := app.DB.QueryRow(`SELECT id::text FROM public.profiles WHERE email = $1 LIMIT 1`, email).Scan(&id)
	if err == nil && id != "" {
		return id, nil
	}
	// 2) auth.users (service role DB ile genelde erişilir)
	err = app.DB.QueryRow(`SELECT id::text FROM auth.users WHERE email = $1 LIMIT 1`, email).Scan(&id)
	return id, err
}

func (app *App) adminUpdateUserPassword(userID, newPassword string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"password": newPassword,
	})
	url := strings.TrimRight(os.Getenv("SUPABASE_URL"), "/")
	// SUPABASE_URL sende rest/v1 ile bitiyorsa düzelt:
	url = strings.Replace(url, "/rest/v1", "", 1)
	url = strings.TrimRight(url, "/") + "/auth/v1/admin/users/" + userID

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	key := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", key)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin update %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (app *App) sendPasswordResetEmail(toEmail, code string) error {
	apiKey := strings.TrimSpace(os.Getenv("re_AUAvNx9Y_DuE7ZX3ZB5HnvPPqAmBtMgtH"))
	if apiKey == "" {
		log.Printf("RESEND_API_KEY yok — mail atılmadı. Kod: %s → %s", toEmail, code)
		return fmt.Errorf("RESEND_API_KEY tanımlı değil")
	}

	from := strings.TrimSpace(os.Getenv("MAIL_FROM"))
	if from == "" {
		from = "DijiAlbüm <onboarding@resend.dev>"
	}

	payload := map[string]interface{}{
		"from":    from,
		"to":      []string{toEmail},
		"subject": "DijiAlbüm şifre sıfırlama kodu",
		"html": fmt.Sprintf(`
			<div style="font-family:Arial,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#0F172A">
			  <h2 style="margin:0 0 12px">DijiAlbüm</h2>
			  <p style="color:#334155">Şifre sıfırlama kodun:</p>
			  <p style="font-size:32px;font-weight:700;letter-spacing:8px;color:#1A73E8;margin:20px 0">%s</p>
			  <p style="font-size:13px;color:#64748B">Kod 15 dakika geçerlidir. Bu isteği sen yapmadıysan yok say.</p>
			</div>
		`, code),
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		log.Printf("Resend hata %d: %s", resp.StatusCode, string(b))
		return fmt.Errorf("mail gönderilemedi: %s", string(b))
	}
	log.Printf("Mail gönderildi → %s", toEmail)
	return nil
}
