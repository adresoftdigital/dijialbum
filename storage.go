package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/disintegration/imaging"
)

func deleteFromSupabaseStorage(publicURL string) error {
	supabaseURL := strings.TrimRight(os.Getenv("SUPABASE_URL"), "/")
	serviceKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	if supabaseURL == "" || serviceKey == "" || publicURL == "" {
		return fmt.Errorf("eksik bilgi")
	}

	// public URL:
	// https://xxx.supabase.co/storage/v1/object/public/media/{path}
	marker := "/storage/v1/object/public/media/"
	idx := strings.Index(publicURL, marker)
	if idx < 0 {
		return fmt.Errorf("path çıkarılamadı: %s", publicURL)
	}
	objectPath := publicURL[idx+len(marker):]

	deleteURL := fmt.Sprintf("%s/storage/v1/object/media/%s", supabaseURL, objectPath)

	req, err := http.NewRequest(http.MethodDelete, deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage delete %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func uploadToSupabaseStorage(path string, data []byte, contentType string) (string, error) {
	supabaseURL := strings.TrimRight(os.Getenv("SUPABASE_URL"), "/")
	serviceKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")

	if supabaseURL == "" || serviceKey == "" {
		return "", fmt.Errorf("SUPABASE_URL veya SUPABASE_SERVICE_ROLE_KEY eksik")
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// path başında / olmasın
	path = strings.TrimPrefix(path, "/")

	// Her segmenti encode et (UUID ve uzantı güvenli kalsın)
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	encodedPath := strings.Join(parts, "/")

	uploadURL := fmt.Sprintf("%s/storage/v1/object/media/%s", supabaseURL, encodedPath)
	log.Printf("📤 Storage upload URL: %s", uploadURL)

	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+serviceKey)
	req.Header.Set("apikey", serviceKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("storage %d: %s", resp.StatusCode, string(body))
	}

	publicURL := fmt.Sprintf("%s/storage/v1/object/public/media/%s", supabaseURL, encodedPath)
	return publicURL, nil
}

func createThumbnail(original []byte) (thumb []byte, width, height int, err error) {
	img, err := imaging.Decode(bytes.NewReader(original))
	if err != nil {
		return nil, 0, 0, err
	}

	bounds := img.Bounds()
	width = bounds.Dx()
	height = bounds.Dy()

	// Çok büyükse önce max 1200px'e indir (RAM koruması)
	if width > 1200 || height > 1200 {
		img = imaging.Fit(img, 1200, 1200, imaging.Lanczos)
	}

	resized := imaging.Resize(img, 400, 0, imaging.Lanczos)

	var buf bytes.Buffer
	if err := imaging.Encode(&buf, resized, imaging.JPEG, imaging.JPEGQuality(75)); err != nil {
		return nil, width, height, err
	}

	return buf.Bytes(), width, height, nil
}
