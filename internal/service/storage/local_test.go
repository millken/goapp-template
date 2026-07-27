package storage_test

import (
	"os"
	"testing"

	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/goapp-template/internal/service/storage/backendtest"
)

func TestLocalBackend_Contract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) storage.Backend {
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		t.Cleanup(func() { _ = root.Close() })
		return storage.NewLocalBackend(root)
	})
}
