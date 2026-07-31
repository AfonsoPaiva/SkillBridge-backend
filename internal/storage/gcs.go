package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"cloud.google.com/go/storage"
	"github.com/paiva/SkillBridge/Backend/config"
)

var GCSClient *storage.Client
var bucketName string

// BucketName returns the configured GCS bucket name.
func BucketName() string { return bucketName }

// InitGCS initializes the Google Cloud Storage client
func InitGCS() error {
	ctx := context.Background()
	
	bucketName = config.AppConfig.GCSBucketName
	if bucketName == "" {
		log.Println("Warning: GCS_BUCKET_NAME not configured. File uploads will fail.")
		return fmt.Errorf("GCS_BUCKET_NAME not configured")
	}

	// Create GCS client
	// Authentication is handled automatically via:
	// 1. GOOGLE_APPLICATION_CREDENTIALS env var (service account JSON file path)
	// 2. Cloud Run's default service account (in production)
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create GCS client: %w", err)
	}

	GCSClient = client
	log.Printf("✓ Google Cloud Storage initialized (bucket: %s)", bucketName)
	return nil
}

// UploadFile uploads a file to Google Cloud Storage
// Parameters:
//   - objectName: the path/name for the file in GCS (e.g., "avatars/user123.jpg")
//   - reader: the file content reader
//   - contentType: MIME type (e.g., "image/jpeg")
//
// Returns the public URL of the uploaded file
func UploadFile(objectName string, reader io.Reader, contentType string) (string, error) {
	if GCSClient == nil {
		return "", fmt.Errorf("GCS client not initialized")
	}

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Second*50)
	defer cancel()

	// Create object writer
	obj := GCSClient.Bucket(bucketName).Object(objectName)
	wc := obj.NewWriter(ctx)
	wc.ContentType = contentType
	wc.CacheControl = "public, max-age=31536000" // Cache for 1 year

	// Copy file content to GCS
	if _, err := io.Copy(wc, reader); err != nil {
		wc.Close()
		return "", fmt.Errorf("failed to upload to GCS: %w", err)
	}

	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("failed to close GCS writer: %w", err)
	}

	// Make the file publicly readable
	// Note: Your bucket must have public access enabled for this to work
	// Alternative: Use signed URLs (see GenerateSignedURL function below)
	acl := obj.ACL()
	if err := acl.Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		log.Printf("Warning: Failed to make object public: %v", err)
		// Don't return error - file is uploaded, just not public
	}

	// Return public URL
	publicURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)
	log.Printf("✓ Uploaded to GCS: %s", publicURL)
	
	return publicURL, nil
}

// DeleteFile removes a file from Google Cloud Storage
func DeleteFile(objectName string) error {
	if GCSClient == nil {
		return fmt.Errorf("GCS client not initialized")
	}

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	obj := GCSClient.Bucket(bucketName).Object(objectName)
	if err := obj.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete from GCS: %w", err)
	}

	log.Printf("✓ Deleted from GCS: %s", objectName)
	return nil
}

// GenerateSignedURL creates a signed URL for private file access
// This is useful if you don't want to make your bucket public
// The URL will be valid for the specified duration
func GenerateSignedURL(objectName string, expiration time.Duration) (string, error) {
	if GCSClient == nil {
		return "", fmt.Errorf("GCS client not initialized")
	}

	opts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(expiration),
	}

	url, err := GCSClient.Bucket(bucketName).SignedURL(objectName, opts)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %w", err)
	}

	return url, nil
}

// ReadObject reads the full content of a GCS object and returns it as bytes.
func ReadObject(objectName string) ([]byte, error) {
	if GCSClient == nil {
		return nil, fmt.Errorf("GCS client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rc, err := GCSClient.Bucket(bucketName).Object(objectName).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open GCS object %s: %w", objectName, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read GCS object %s: %w", objectName, err)
	}

	return data, nil
}

// WriteObject writes bytes to a GCS object, overwriting any existing content.
func WriteObject(objectName string, data []byte, contentType string) error {
	if GCSClient == nil {
		return fmt.Errorf("GCS client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	obj := GCSClient.Bucket(bucketName).Object(objectName)
	wc := obj.NewWriter(ctx)
	wc.ContentType = contentType
	wc.CacheControl = "no-cache" // Always serve fresh

	if _, err := io.Copy(wc, bytes.NewReader(data)); err != nil {
		wc.Close()
		return fmt.Errorf("failed to write GCS object %s: %w", objectName, err)
	}

	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close GCS writer for %s: %w", objectName, err)
	}

	log.Printf("✓ Written to GCS: %s (%d bytes)", objectName, len(data))
	return nil
}

// CloseGCS closes the GCS client gracefully
func CloseGCS() {
	if GCSClient != nil {
		GCSClient.Close()
		log.Println("✓ GCS client closed")
	}
}
