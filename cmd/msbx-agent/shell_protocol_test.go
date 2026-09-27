package main

import "testing"

func TestValidateShellProtocolAcceptsCurrentVersion(t *testing.T) {
	if err := validateShellProtocol(shellProtocolMagic); err != nil {
		t.Fatalf("validateShellProtocol(%q) = %v, want nil", shellProtocolMagic, err)
	}
}

func TestValidateShellProtocolRejectsOlderVersion(t *testing.T) {
	if err := validateShellProtocol("MSBX/7"); err == nil {
		t.Fatal("validateShellProtocol(MSBX/7) = nil, want version mismatch error")
	}
}

func TestValidateShellProtocolRejectsUnknownVersion(t *testing.T) {
	if err := validateShellProtocol("MSBX/999"); err == nil {
		t.Fatal("validateShellProtocol(MSBX/999) = nil, want version mismatch error")
	}
}
