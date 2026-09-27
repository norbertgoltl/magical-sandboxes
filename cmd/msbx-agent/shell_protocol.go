package main

import "fmt"

const shellProtocolMagic = "MSBX/8"

func validateShellProtocol(magic string) error {
	if magic != shellProtocolMagic {
		return fmt.Errorf("unsupported protocol: %q (expected %q)", magic, shellProtocolMagic)
	}
	return nil
}
