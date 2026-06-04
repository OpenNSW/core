package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/loaders/local"
)

func TestLocalFileLoader(t *testing.T) {
	tempDir := t.TempDir()
	loader := local.New(tempDir)

	t.Run("Load existing file", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "test.json")
		expected := []byte(`{"hello": "world"}`)
		if err := os.WriteFile(filePath, expected, 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		data, err := loader.Load(context.Background(), "test.json")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if string(data) != string(expected) {
			t.Errorf("expected %q, got %q", expected, data)
		}
	})

	t.Run("Load missing file returns ErrNotFound", func(t *testing.T) {
		_, err := loader.Load(context.Background(), "missing.json")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("Load file escaping root returns ErrNotFound", func(t *testing.T) {
		_, err := loader.Load(context.Background(), "../local_test.go")
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("expected ErrNotFound for traversing path, got %v", err)
		}
	})
}
