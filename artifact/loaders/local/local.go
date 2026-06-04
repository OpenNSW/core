package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/OpenNSW/core/artifact"
)

type LocalFileLoader struct {
	Root string
}

func New(root string) LocalFileLoader {
	return LocalFileLoader{Root: root}
}

func (l LocalFileLoader) Load(ctx context.Context, path string) ([]byte, error) {
	fullPath := filepath.Join(l.Root, path)
	rel, err := filepath.Rel(l.Root, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("%w: path %q escapes root %q", artifact.ErrNotFound, path, l.Root)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: local file not found at %s", artifact.ErrNotFound, fullPath)
		}
		return nil, fmt.Errorf("read local file %s: %w", fullPath, err)
	}
	return data, nil
}
