package main

import (
	"os"
	"path/filepath"
	"testing"
)

func isolateVMResourceEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{vmCPUsEnv, vmMemoryMiBEnv} {
		value, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func writeProjectEnv(t *testing.T, projectPath, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectPath, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
}

func TestLoadVMResourcesUsesSmallDefaultsWithoutEnvFile(t *testing.T) {
	isolateVMResourceEnvironment(t)
	resources, err := loadVMResources(t.TempDir())
	if err != nil {
		t.Fatalf("loadVMResources() error = %v", err)
	}
	if resources != (vmResources{cpus: 2, memoryMiB: 4096}) {
		t.Fatalf("loadVMResources() = %+v, want Small defaults", resources)
	}
}

func TestLoadVMResourcesReadsProjectEnvValues(t *testing.T) {
	isolateVMResourceEnvironment(t)
	projectPath := t.TempDir()
	writeProjectEnv(t, projectPath, "# Medium example\nMSBX_VM_CPUS=4\nMSBX_VM_MEMORY_MIB=8192\nOTHER_SETTING=value\n")

	resources, err := loadVMResources(projectPath)
	if err != nil {
		t.Fatalf("loadVMResources() error = %v", err)
	}
	if resources != (vmResources{cpus: 4, memoryMiB: 8192}) {
		t.Fatalf("loadVMResources() = %+v, want 4 CPUs and 8192 MiB", resources)
	}
}

func TestLoadVMResourcesEnvironmentOverridesProjectEnv(t *testing.T) {
	isolateVMResourceEnvironment(t)
	projectPath := t.TempDir()
	writeProjectEnv(t, projectPath, "MSBX_VM_CPUS=4\nMSBX_VM_MEMORY_MIB=8192\n")
	t.Setenv(vmCPUsEnv, "1")

	resources, err := loadVMResources(projectPath)
	if err != nil {
		t.Fatalf("loadVMResources() error = %v", err)
	}
	if resources != (vmResources{cpus: 1, memoryMiB: 8192}) {
		t.Fatalf("loadVMResources() = %+v, want environment CPU override and .env memory", resources)
	}
}

func TestLoadVMResourcesRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "non-numeric CPU count", contents: "MSBX_VM_CPUS=two\n"},
		{name: "zero CPU count", contents: "MSBX_VM_CPUS=0\n"},
		{name: "memory below minimum", contents: "MSBX_VM_MEMORY_MIB=512\n"},
		{name: "non-numeric memory", contents: "MSBX_VM_MEMORY_MIB=large\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolateVMResourceEnvironment(t)
			projectPath := t.TempDir()
			writeProjectEnv(t, projectPath, test.contents)
			if _, err := loadVMResources(projectPath); err == nil {
				t.Fatal("loadVMResources() error = nil, want invalid configuration error")
			}
		})
	}
}
