package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultVMCPUs      = 2
	defaultVMMemoryMiB = 4096
	vmCPUsEnv          = "MSBX_VM_CPUS"
	vmMemoryMiBEnv     = "MSBX_VM_MEMORY_MIB"
)

type vmResources struct {
	cpus      int
	memoryMiB int
}

func loadVMResources(projectPath string) (vmResources, error) {
	resources := vmResources{cpus: defaultVMCPUs, memoryMiB: defaultVMMemoryMiB}
	values := make(map[string]string, 2)

	envPath := filepath.Join(projectPath, ".env")
	file, err := os.Open(envPath)
	if err != nil && !os.IsNotExist(err) {
		return vmResources{}, fmt.Errorf("unable to read project environment file %s: %w", envPath, err)
	}
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return vmResources{}, fmt.Errorf("invalid entry in %s at line %d; expected KEY=VALUE", envPath, lineNumber)
			}
			key = strings.TrimSpace(key)
			if key == vmCPUsEnv || key == vmMemoryMiBEnv {
				values[key] = strings.TrimSpace(value)
			}
		}
		if err := scanner.Err(); err != nil {
			return vmResources{}, fmt.Errorf("unable to read project environment file %s: %w", envPath, err)
		}
	}

	if value, ok := os.LookupEnv(vmCPUsEnv); ok {
		values[vmCPUsEnv] = value
	}
	if value, ok := os.LookupEnv(vmMemoryMiBEnv); ok {
		values[vmMemoryMiBEnv] = value
	}

	if value, ok := values[vmCPUsEnv]; ok {
		cpus, err := strconv.Atoi(value)
		if err != nil || cpus < 1 {
			return vmResources{}, fmt.Errorf("%s must be a positive integer", vmCPUsEnv)
		}
		resources.cpus = cpus
	}
	if value, ok := values[vmMemoryMiBEnv]; ok {
		memoryMiB, err := strconv.Atoi(value)
		if err != nil || memoryMiB < 1024 {
			return vmResources{}, fmt.Errorf("%s must be an integer of at least 1024", vmMemoryMiBEnv)
		}
		resources.memoryMiB = memoryMiB
	}

	return resources, nil
}
