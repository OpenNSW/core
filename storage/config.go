package storage

import (
	"fmt"
	"time"

	"github.com/OpenNSW/nsw/backend/internal/validation"
)

type Config struct {
	Type           string // "local" or "s3"
	LocalBaseDir   string
	LocalPublicURL string
	S3Endpoint     string
	S3Bucket       string
	S3Region       string
	S3AccessKey    string
	S3SecretKey    string
	S3UseSSL       bool
	S3PublicURL    string
	LocalPutSecret string
	PresignTTL     time.Duration
}

func (c Config) Validate() error {
	switch c.Type {
	case "local":
		if c.LocalBaseDir == "" {
			return fmt.Errorf("STORAGE_LOCAL_BASE_DIR is required when STORAGE_TYPE=local")
		}
		if c.LocalPublicURL == "" {
			return fmt.Errorf("STORAGE_LOCAL_PUBLIC_URL is required when STORAGE_TYPE=local")
		}
		if err := validation.HTTPURL("STORAGE_LOCAL_PUBLIC_URL", c.LocalPublicURL); err != nil {
			return err
		}
		if c.LocalPutSecret == "" {
			return fmt.Errorf("STORAGE_LOCAL_PUT_SECRET is required when STORAGE_TYPE=local")
		}
	case "s3":
		if c.S3Endpoint == "" {
			return fmt.Errorf("STORAGE_S3_ENDPOINT is required when STORAGE_TYPE=s3")
		}
		if err := validation.HTTPURL("STORAGE_S3_ENDPOINT", c.S3Endpoint); err != nil {
			return err
		}
		if c.S3Bucket == "" {
			return fmt.Errorf("STORAGE_S3_BUCKET is required when STORAGE_TYPE=s3")
		}
		if c.S3Region == "" {
			return fmt.Errorf("STORAGE_S3_REGION is required when STORAGE_TYPE=s3")
		}
		if (c.S3AccessKey == "") != (c.S3SecretKey == "") {
			return fmt.Errorf("STORAGE_S3_ACCESS_KEY and STORAGE_S3_SECRET_KEY must be configured together")
		}
		if c.S3PublicURL != "" {
			if err := validation.HTTPURL("STORAGE_S3_PUBLIC_URL", c.S3PublicURL); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported STORAGE_TYPE: %s", c.Type)
	}

	if c.PresignTTL <= 0 {
		return fmt.Errorf("STORAGE_PRESIGN_TTL must be greater than zero")
	}

	return nil
}
