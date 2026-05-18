package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistry_Scan(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := NewRegistry(tmpDir)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// Test case 1: Empty directory
	err = reg.Scan("")
	if err != nil {
		t.Errorf("Scan failed: %v", err)
	}
	if len(reg.services) != 0 {
		t.Errorf("expected no services in empty directory, got %d", len(reg.services))
	}

	// Test case 2: Add services and nodes
	// Structure:
	// tmpDir/
	//   api/
	//     v1.sock
	//     v2.sock
	//   web/
	//     app.sock

	apiDir := filepath.Join(tmpDir, "api")
	webDir := filepath.Join(tmpDir, "web")
	os.MkdirAll(apiDir, 0755)
	os.MkdirAll(webDir, 0755)

	v1Path := filepath.Join(apiDir, "v1.sock")
	v2Path := filepath.Join(apiDir, "v2.sock")
	appPath := filepath.Join(webDir, "app.sock")

	os.WriteFile(v1Path, []byte(""), 0644)
	os.WriteFile(v2Path, []byte(""), 0644)
	os.WriteFile(appPath, []byte(""), 0644)

	err = reg.Scan("")
	if err != nil {
		t.Errorf("Scan failed: %v", err)
	}

	if len(reg.services) != 2 {
		t.Errorf("expected 2 services, got %d", len(reg.services))
	}

	if ss, ok := reg.services["api"]; !ok || len(ss.nodes) != 2 {
		t.Errorf("expected 2 nodes for api service, got %v", ss)
	}

	// Test case 3: Targeted scan
	os.Remove(v1Path)
	err = reg.Scan("api")
	if err != nil {
		t.Errorf("ScanService failed: %v", err)
	}

	if ss, ok := reg.services["api"]; !ok || len(ss.nodes) != 1 {
		t.Errorf("expected 1 node for api service after removal and targeted scan, got %v", ss)
	} else {
		// Check if the path is relative (doesn't start with / or C:)
		nodePath := ss.nodes[0]
		if !filepath.IsAbs(nodePath) {
			t.Errorf("expected absolute node path, got relative: %s", nodePath)
		}
	}

	// Test case 4: Remove all nodes of a service
	os.Remove(v2Path)
	err = reg.Scan("api")
	if err != nil {
		t.Errorf("ScanService failed: %v", err)
	}

	if _, ok := reg.services["api"]; ok {
		t.Errorf("expected api service to be removed after all nodes are gone")
	}
}
